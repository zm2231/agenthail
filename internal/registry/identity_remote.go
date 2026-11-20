package registry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"database/sql"

	"github.com/zm2231/agenthail/internal/cfaccess"
)

const RemoteTokenPrefix = "ahr_"

var ErrRemoteUnknown = errors.New("no identity holds this remote token")

type RemoteIdentity struct {
	Name       string    `json:"name"`
	IdentityID string    `json:"identityId"`
	ClientID   string    `json:"accessClientId,omitempty"`
	Email      string    `json:"accessEmail,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeen   time.Time `json:"lastSeen,omitzero"`
}

func (r RemoteIdentity) Binding() string {
	switch {
	case r.ClientID != "":
		return "service token " + r.ClientID
	case r.Email != "":
		return "email " + r.Email
	}
	return "token only"
}

func remoteTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newRemoteToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return RemoteTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (r *Registry) BindRemoteIdentity(name, clientID, email string) (RemoteIdentity, string, error) {
	clientID, email = cfaccess.ClientID(clientID), strings.ToLower(strings.TrimSpace(email))
	if clientID != "" && email != "" {
		return RemoteIdentity{}, "", errors.New("bind a service token's Client ID or an email, not both")
	}
	return r.issueRemoteToken(name, RemoteIdentity{ClientID: clientID, Email: email})
}

func (r *Registry) RotateRemoteIdentity(name string) (RemoteIdentity, string, error) {
	current, err := r.RemoteIdentityNamed(name)
	if err != nil {
		return RemoteIdentity{}, "", err
	}
	return r.issueRemoteToken(name, current)
}

func (r *Registry) issueRemoteToken(name string, binding RemoteIdentity) (RemoteIdentity, string, error) {
	token, err := newRemoteToken()
	if err != nil {
		return RemoteIdentity{}, "", err
	}
	now := r.now()
	tx, err := r.db.Begin()
	if err != nil {
		return RemoteIdentity{}, "", err
	}
	defer tx.Rollback()
	id, err := agentIdentityIDTx(tx, name)
	if err != nil {
		return RemoteIdentity{}, "", err
	}
	if _, err := tx.Exec(`INSERT INTO identity_remote(identity_id,token_hash,access_client_id,access_email,created_at_ms) VALUES(?,?,?,?,?)
		ON CONFLICT(identity_id) DO UPDATE SET token_hash=excluded.token_hash,access_client_id=excluded.access_client_id,access_email=excluded.access_email,created_at_ms=excluded.created_at_ms,last_seen_at_ms=0`,
		id, remoteTokenHash(token), binding.ClientID, binding.Email, now.UnixMilli()); err != nil {
		return RemoteIdentity{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return RemoteIdentity{}, "", err
	}
	remote, err := r.RemoteIdentityNamed(name)
	return remote, token, err
}

func (r *Registry) UnbindRemoteIdentity(name string) error {
	id, err := agentIdentityIDTx(r.read, name)
	if err != nil {
		return err
	}
	result, err := r.db.Exec(`DELETE FROM identity_remote WHERE identity_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("@%s has no remote access to turn off", strings.TrimPrefix(name, "@"))
	}
	return nil
}

const remoteIdentitySelect = `SELECT i.name,i.identity_id,r.access_client_id,r.access_email,r.created_at_ms,r.last_seen_at_ms FROM identity_remote r JOIN agent_identities i ON i.identity_id=r.identity_id`

func scanRemoteIdentity(row rowScanner) (RemoteIdentity, error) {
	var remote RemoteIdentity
	var created, seen int64
	if err := row.Scan(&remote.Name, &remote.IdentityID, &remote.ClientID, &remote.Email, &created, &seen); err != nil {
		return RemoteIdentity{}, err
	}
	remote.CreatedAt = time.UnixMilli(created).UTC()
	if seen > 0 {
		remote.LastSeen = time.UnixMilli(seen).UTC()
	}
	return remote, nil
}

func (r *Registry) RemoteIdentities() ([]RemoteIdentity, error) {
	return queryRows(r.read, []RemoteIdentity{}, scanRemoteIdentity, remoteIdentitySelect+` ORDER BY i.name`)
}

func (r *Registry) RemoteIdentityNamed(name string) (RemoteIdentity, error) {
	remote, err := scanRemoteIdentity(r.read.QueryRow(remoteIdentitySelect+` WHERE i.name=?`, strings.TrimPrefix(name, "@")))
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteIdentity{}, fmt.Errorf("@%s has no remote access; turn it on with: agenthail identity remote %s", strings.TrimPrefix(name, "@"), strings.TrimPrefix(name, "@"))
	}
	return remote, err
}

func (r *Registry) RemoteIdentityByToken(token string) (RemoteIdentity, error) {
	if !strings.HasPrefix(token, RemoteTokenPrefix) {
		return RemoteIdentity{}, ErrRemoteUnknown
	}
	remote, err := scanRemoteIdentity(r.read.QueryRow(remoteIdentitySelect+` WHERE r.token_hash=?`, remoteTokenHash(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteIdentity{}, ErrRemoteUnknown
	}
	return remote, err
}

func (r *Registry) TouchRemoteIdentity(identityID string, now time.Time) error {
	_, err := r.db.Exec(`UPDATE identity_remote SET last_seen_at_ms=? WHERE identity_id=? AND last_seen_at_ms<?`, now.UnixMilli(), identityID, now.Add(-time.Minute).UnixMilli())
	return err
}

func (r *Registry) AccessSettings() (team, aud string, ok bool, err error) {
	err = r.read.QueryRow(`SELECT team,aud FROM mcp_access WHERE id=1`).Scan(&team, &aud)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return team, aud, err == nil, err
}

func (r *Registry) SetAccessSettings(team, aud string) error {
	team, aud = strings.TrimSpace(team), strings.TrimSpace(aud)
	if team == "" || aud == "" {
		return errors.New("both the Access team and the application's AUD tag are required")
	}
	_, err := r.db.Exec(`INSERT INTO mcp_access(id,team,aud,changed_at_ms) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET team=excluded.team,aud=excluded.aud,changed_at_ms=excluded.changed_at_ms,keys_read_at_ms=0,keys_error=''`, team, aud, r.now().UnixMilli())
	return err
}

type AccessKeys struct {
	ReadAt time.Time `json:"readAt,omitzero"`
	Error  string    `json:"error,omitempty"`
}

func (r *Registry) RecordAccessKeys(readAt time.Time, readErr error) error {
	message := ""
	if readErr != nil {
		message = readErr.Error()
	}
	var at int64
	if !readAt.IsZero() {
		at = readAt.UnixMilli()
	}
	_, err := r.db.Exec(`UPDATE mcp_access SET keys_read_at_ms=MAX(keys_read_at_ms,?),keys_error=? WHERE id=1`, at, message)
	return err
}

func (r *Registry) AccessKeysStatus() (AccessKeys, error) {
	var at int64
	var status AccessKeys
	err := r.read.QueryRow(`SELECT keys_read_at_ms,keys_error FROM mcp_access WHERE id=1`).Scan(&at, &status.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessKeys{}, nil
	}
	if at > 0 {
		status.ReadAt = time.UnixMilli(at).UTC()
	}
	return status, err
}

func (r *Registry) ClearAccessSettings() error {
	_, err := r.db.Exec(`DELETE FROM mcp_access WHERE id=1`)
	return err
}
