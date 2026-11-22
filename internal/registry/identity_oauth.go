package registry

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	OAuthAccessPrefix   = "aho_"
	OAuthRefreshPrefix  = "ahf_"
	OAuthClientPrefix   = "ahc_"
	AskOriginOAuthGrant = "oauth-grant"
	// A grant's event subscriptions are keyed to the grant, since its tokens rotate.
	OAuthCredentialPrefix = "oauth:"
)

var (
	ErrOAuthUnknown       = errors.New("no OAuth grant holds this token")
	ErrOAuthRefreshReused = errors.New("this refresh token was already used, so its grant is revoked")
	ErrOAuthClientLimit   = errors.New("too many clients are registered and not yet approved")
	ErrOAuthClientUnknown = errors.New("no client is registered with this id")
)

type OAuthClient struct {
	ID           string    `json:"clientId"`
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirectUris"`
	CreatedAt    time.Time `json:"createdAt"`
}

type OAuthGrant struct {
	ID             string    `json:"id"`
	IdentityID     string    `json:"identityId"`
	Identity       string    `json:"identity"`
	ClientID       string    `json:"clientId"`
	ClientName     string    `json:"clientName"`
	CreatedAt      time.Time `json:"createdAt"`
	LastSeen       time.Time `json:"lastSeen,omitzero"`
	RefreshExpires time.Time `json:"refreshExpires"`
}

func (g OAuthGrant) Credential() string { return OAuthCredentialPrefix + g.ID }

type OAuthTokens struct {
	Access         string
	Refresh        string
	AccessExpires  time.Time
	RefreshExpires time.Time
	Grant          OAuthGrant
}

type OAuthLifetimes struct {
	Access  time.Duration
	Refresh time.Duration
}

func randomToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (r *Registry) SetOAuthIssuer(issuer string) error {
	_, err := r.db.Exec(`INSERT INTO mcp_oauth(id,issuer,changed_at_ms) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET issuer=excluded.issuer,changed_at_ms=excluded.changed_at_ms WHERE issuer<>excluded.issuer`, issuer, r.now().UnixMilli())
	return err
}

func (r *Registry) ClearOAuthIssuer() error {
	_, err := r.db.Exec(`DELETE FROM mcp_oauth WHERE id=1`)
	return err
}

func (r *Registry) OAuthIssuer() (string, bool, error) {
	var issuer string
	err := r.read.QueryRow(`SELECT issuer FROM mcp_oauth WHERE id=1`).Scan(&issuer)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return issuer, err == nil, err
}

// Registering grants nothing lasting: clients nobody approved are pruned and capped.
func (r *Registry) RegisterOAuthClient(name string, redirectURIs []string, limit int, staleAfter time.Duration) (OAuthClient, error) {
	id, err := randomToken(OAuthClientPrefix)
	if err != nil {
		return OAuthClient{}, err
	}
	encoded, err := json.Marshal(redirectURIs)
	if err != nil {
		return OAuthClient{}, err
	}
	now := r.now()
	tx, err := r.db.Begin()
	if err != nil {
		return OAuthClient{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM oauth_clients WHERE approved_at_ms=0 AND created_at_ms<?`, now.Add(-staleAfter).UnixMilli()); err != nil {
		return OAuthClient{}, err
	}
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM oauth_clients WHERE approved_at_ms=0`).Scan(&pending); err != nil {
		return OAuthClient{}, err
	}
	if pending >= limit {
		return OAuthClient{}, ErrOAuthClientLimit
	}
	if _, err := tx.Exec(`INSERT INTO oauth_clients(client_id,name,redirect_uris,created_at_ms) VALUES(?,?,?,?)`, id, name, string(encoded), now.UnixMilli()); err != nil {
		return OAuthClient{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthClient{}, err
	}
	return OAuthClient{ID: id, Name: name, RedirectURIs: redirectURIs, CreatedAt: time.UnixMilli(now.UnixMilli()).UTC()}, nil
}

func (r *Registry) OAuthClient(id string) (OAuthClient, error) {
	var client OAuthClient
	var redirects string
	var created int64
	err := r.read.QueryRow(`SELECT client_id,name,redirect_uris,created_at_ms FROM oauth_clients WHERE client_id=?`, id).Scan(&client.ID, &client.Name, &redirects, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClient{}, ErrOAuthClientUnknown
	}
	if err != nil {
		return OAuthClient{}, err
	}
	client.CreatedAt = time.UnixMilli(created).UTC()
	return client, json.Unmarshal([]byte(redirects), &client.RedirectURIs)
}

func newOAuthTokens(now time.Time, life OAuthLifetimes) (OAuthTokens, error) {
	access, err := randomToken(OAuthAccessPrefix)
	if err != nil {
		return OAuthTokens{}, err
	}
	refresh, err := randomToken(OAuthRefreshPrefix)
	if err != nil {
		return OAuthTokens{}, err
	}
	at := time.UnixMilli(now.UnixMilli()).UTC()
	return OAuthTokens{Access: access, Refresh: refresh, AccessExpires: at.Add(life.Access), RefreshExpires: at.Add(life.Refresh)}, nil
}

func (r *Registry) IssueOAuthGrant(identityID, clientID string, life OAuthLifetimes) (OAuthTokens, error) {
	now := r.now()
	tokens, err := newOAuthTokens(now, life)
	if err != nil {
		return OAuthTokens{}, err
	}
	id, err := randomToken("")
	if err != nil {
		return OAuthTokens{}, err
	}
	id = id[:16]
	tx, err := r.db.Begin()
	if err != nil {
		return OAuthTokens{}, err
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM identity_oauth WHERE identity_id=?`, identityID).Scan(&enabled); err != nil {
		return OAuthTokens{}, err
	}
	if enabled == 0 {
		return OAuthTokens{}, errors.New("this identity is not enabled for OAuth sign-in")
	}
	result, err := tx.Exec(`UPDATE oauth_clients SET approved_at_ms=CASE WHEN approved_at_ms=0 THEN ? ELSE approved_at_ms END WHERE client_id=?`, now.UnixMilli(), clientID)
	if err != nil {
		return OAuthTokens{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return OAuthTokens{}, ErrOAuthClientUnknown
	}
	if _, err := tx.Exec(`INSERT INTO identity_oauth_grants(grant_id,identity_id,client_id,access_hash,access_expires_at_ms,refresh_hash,refresh_expires_at_ms,created_at_ms) VALUES(?,?,?,?,?,?,?,?)`,
		id, identityID, clientID, remoteTokenHash(tokens.Access), tokens.AccessExpires.UnixMilli(), remoteTokenHash(tokens.Refresh), tokens.RefreshExpires.UnixMilli(), now.UnixMilli()); err != nil {
		return OAuthTokens{}, err
	}
	if tokens.Grant, err = scanOAuthGrant(tx.QueryRow(oauthGrantSelect+` WHERE g.grant_id=?`, id)); err != nil {
		return OAuthTokens{}, err
	}
	return tokens, tx.Commit()
}

// A rotated refresh token presented again means a copy exists, so its grant is revoked.
func (r *Registry) RefreshOAuthGrant(refresh, clientID string, life OAuthLifetimes) (OAuthTokens, error) {
	now := r.now()
	hash := remoteTokenHash(refresh)
	tx, err := r.db.Begin()
	if err != nil {
		return OAuthTokens{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM oauth_spent_refresh WHERE spent_at_ms<?`, now.Add(-life.Refresh).UnixMilli()); err != nil {
		return OAuthTokens{}, err
	}
	grant, err := scanOAuthGrant(tx.QueryRow(oauthGrantSelect+` WHERE g.refresh_hash=? AND g.client_id=? AND g.refresh_expires_at_ms>?`, hash, clientID, now.UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		reused, err := scanOAuthGrant(tx.QueryRow(oauthGrantSelect+` JOIN oauth_spent_refresh s ON s.grant_id=g.grant_id WHERE s.refresh_hash=? AND g.client_id=?`, hash, clientID))
		if errors.Is(err, sql.ErrNoRows) {
			return OAuthTokens{}, ErrOAuthUnknown
		}
		if err != nil {
			return OAuthTokens{}, err
		}
		if _, err := tx.Exec(`DELETE FROM identity_oauth_grants WHERE grant_id=?`, reused.ID); err != nil {
			return OAuthTokens{}, err
		}
		if err := tx.Commit(); err != nil {
			return OAuthTokens{}, err
		}
		return OAuthTokens{Grant: reused}, ErrOAuthRefreshReused
	}
	if err != nil {
		return OAuthTokens{}, err
	}
	tokens, err := newOAuthTokens(now, life)
	if err != nil {
		return OAuthTokens{}, err
	}
	if _, err := tx.Exec(`INSERT INTO oauth_spent_refresh(refresh_hash,grant_id,spent_at_ms) VALUES(?,?,?)`, hash, grant.ID, now.UnixMilli()); err != nil {
		return OAuthTokens{}, err
	}
	if _, err := tx.Exec(`UPDATE identity_oauth_grants SET access_hash=?,access_expires_at_ms=?,refresh_hash=?,refresh_expires_at_ms=? WHERE grant_id=?`,
		remoteTokenHash(tokens.Access), tokens.AccessExpires.UnixMilli(), remoteTokenHash(tokens.Refresh), tokens.RefreshExpires.UnixMilli(), grant.ID); err != nil {
		return OAuthTokens{}, err
	}
	grant.RefreshExpires = tokens.RefreshExpires
	tokens.Grant = grant
	return tokens, tx.Commit()
}

func (r *Registry) OAuthGrantByAccess(token string) (OAuthGrant, time.Time, error) {
	if !strings.HasPrefix(token, OAuthAccessPrefix) {
		return OAuthGrant{}, time.Time{}, ErrOAuthUnknown
	}
	var expires int64
	grant, err := scanOAuthGrant(r.read.QueryRow(`SELECT `+oauthGrantColumns+`,g.access_expires_at_ms`+oauthGrantFrom+` WHERE g.access_hash=? AND g.access_expires_at_ms>?`, remoteTokenHash(token), r.now().UnixMilli()), &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthGrant{}, time.Time{}, ErrOAuthUnknown
	}
	return grant, time.UnixMilli(expires).UTC(), err
}

func (r *Registry) TouchOAuthGrant(id string, now time.Time) error {
	_, err := r.db.Exec(`UPDATE identity_oauth_grants SET last_seen_at_ms=? WHERE grant_id=? AND last_seen_at_ms<?`, now.UnixMilli(), id, now.Add(-time.Minute).UnixMilli())
	return err
}

// SetIdentityOAuth turns OAuth sign-in on or off for an identity with remote
// access; off revokes its grants and reports how many.
func (r *Registry) SetIdentityOAuth(name string, on bool) (int, error) {
	name = strings.TrimPrefix(name, "@")
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := agentIdentityIDTx(tx, name)
	if err != nil {
		return 0, err
	}
	var remote int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM identity_remote WHERE identity_id=?`, id).Scan(&remote); err != nil {
		return 0, err
	}
	if remote == 0 {
		return 0, fmt.Errorf("@%s has no remote access; turn it on first with: agenthail identity remote %s", name, name)
	}
	if on {
		_, err := tx.Exec(`INSERT INTO identity_oauth(identity_id,enabled_at_ms) VALUES(?,?) ON CONFLICT(identity_id) DO NOTHING`, id, r.now().UnixMilli())
		if err != nil {
			return 0, err
		}
		return 0, tx.Commit()
	}
	if _, err := tx.Exec(`DELETE FROM identity_oauth WHERE identity_id=?`, id); err != nil {
		return 0, err
	}
	result, err := tx.Exec(`DELETE FROM identity_oauth_grants WHERE identity_id=?`, id)
	if err != nil {
		return 0, err
	}
	revoked, _ := result.RowsAffected()
	return int(revoked), tx.Commit()
}

func (r *Registry) OAuthGrants() ([]OAuthGrant, error) {
	return queryRows(r.read, []OAuthGrant{}, func(row rowScanner) (OAuthGrant, error) { return scanOAuthGrant(row) }, oauthGrantSelect+` ORDER BY i.name,g.created_at_ms`)
}

func (r *Registry) RevokeOAuthGrant(id string) (OAuthGrant, error) {
	grant, err := scanOAuthGrant(r.read.QueryRow(oauthGrantSelect+` WHERE g.grant_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthGrant{}, fmt.Errorf("no OAuth grant %s; list them with: agenthail identity oauth list", id)
	}
	if err != nil {
		return OAuthGrant{}, err
	}
	_, err = r.db.Exec(`DELETE FROM identity_oauth_grants WHERE grant_id=?`, id)
	return grant, err
}

func (r *Registry) RevokeOAuthToken(token, clientID string) (OAuthGrant, bool, error) {
	hash := remoteTokenHash(token)
	grant, err := scanOAuthGrant(r.read.QueryRow(oauthGrantSelect+` WHERE (g.access_hash=?1 OR g.refresh_hash=?1) AND g.client_id=?2`, hash, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthGrant{}, false, nil
	}
	if err != nil {
		return OAuthGrant{}, false, err
	}
	result, err := r.db.Exec(`DELETE FROM identity_oauth_grants WHERE grant_id=?`, grant.ID)
	if err != nil {
		return OAuthGrant{}, false, err
	}
	n, _ := result.RowsAffected()
	return grant, n > 0, nil
}

const (
	oauthGrantColumns = `g.grant_id,g.identity_id,i.name,g.client_id,c.name,g.created_at_ms,g.last_seen_at_ms,g.refresh_expires_at_ms`
	oauthGrantFrom    = ` FROM identity_oauth_grants g JOIN agent_identities i ON i.identity_id=g.identity_id JOIN oauth_clients c ON c.client_id=g.client_id`
	oauthGrantSelect  = `SELECT ` + oauthGrantColumns + oauthGrantFrom
)

func scanOAuthGrant(row rowScanner, extra ...any) (OAuthGrant, error) {
	var grant OAuthGrant
	var created, seen, refresh int64
	targets := append([]any{&grant.ID, &grant.IdentityID, &grant.Identity, &grant.ClientID, &grant.ClientName, &created, &seen, &refresh}, extra...)
	if err := row.Scan(targets...); err != nil {
		return OAuthGrant{}, err
	}
	grant.CreatedAt, grant.RefreshExpires = time.UnixMilli(created).UTC(), time.UnixMilli(refresh).UTC()
	if seen > 0 {
		grant.LastSeen = time.UnixMilli(seen).UTC()
	}
	return grant, nil
}
