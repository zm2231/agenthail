package registry

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	SessionTokenPrefix     = "ahs_"
	CredentialIdleLifetime = 24 * time.Hour
	CredentialMaxLifetime  = 30 * 24 * time.Hour
)

const (
	RevokedRotated        = "rotated"
	RevokedSessionArchive = "session_archived"
	RevokedSessionMoved   = "session_moved"
	RevokedByPerson       = "revoked"
)

var (
	ErrCredentialUnknown    = errors.New("unknown session credential")
	ErrCredentialExpired    = errors.New("session credential expired")
	ErrCredentialMaxExpired = errors.New("session credential reached its maximum lifetime")
)

type CredentialRevokedError struct{ Reason string }

func (e CredentialRevokedError) Error() string {
	return "session credential revoked: " + e.Reason
}

type SessionCredential struct {
	ID            int64
	SessionID     string
	Capabilities  []string
	Ceiling       json.RawMessage
	CreatedAt     time.Time
	LastUsedAt    time.Time
	IdleExpiresAt time.Time
	MaxExpiresAt  time.Time
}

// IssuedCredential carries the raw token only when one was minted; a reused credential's token is never recoverable.
type IssuedCredential struct {
	Credential SessionCredential
	Token      string
}

func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return SessionTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (r *Registry) IssueSessionCredential(sessionID string, capabilities []string, ceiling json.RawMessage, now time.Time) (IssuedCredential, error) {
	capabilities = slices.Sorted(slices.Values(capabilities))
	capabilities = slices.Compact(capabilities)
	encodedCaps, err := json.Marshal(capabilities)
	if err != nil {
		return IssuedCredential{}, err
	}
	encodedCeiling, err := canonicalCeiling(ceiling)
	if err != nil {
		return IssuedCredential{}, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return IssuedCredential{}, err
	}
	defer tx.Rollback()
	var hostID string
	var archived int
	if err := tx.QueryRow(`SELECT s.host_id,EXISTS(SELECT 1 FROM session_archive a WHERE a.session_id=s.id) FROM sessions s WHERE s.id=?`, sessionID).Scan(&hostID, &archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IssuedCredential{}, fmt.Errorf("session %s is not registered", sessionID)
		}
		return IssuedCredential{}, err
	}
	if hostID != "" || archived != 0 {
		return IssuedCredential{}, fmt.Errorf("session %s is not a live local session", sessionID)
	}
	row, err := scanCredential(tx.QueryRow(credentialSelect+` WHERE session_id=? AND revoked_at_ms=0`, sessionID))
	live := row.SessionCredential
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return IssuedCredential{}, err
	case live.MaxExpiresAt.After(now) && string(mustJSON(live.Capabilities)) == string(encodedCaps) && string(live.Ceiling) == string(encodedCeiling):
		idle := now.Add(CredentialIdleLifetime)
		if _, err := tx.Exec(`UPDATE session_credentials SET idle_expires_at_ms=? WHERE id=?`, idle.UnixMilli(), live.ID); err != nil {
			return IssuedCredential{}, err
		}
		live.IdleExpiresAt = time.UnixMilli(idle.UnixMilli())
		return IssuedCredential{Credential: live}, tx.Commit()
	default:
		if _, err := tx.Exec(`UPDATE session_credentials SET revoked_at_ms=?,revoked_reason=? WHERE id=?`, now.UnixMilli(), RevokedRotated, live.ID); err != nil {
			return IssuedCredential{}, err
		}
	}
	token, err := newSessionToken()
	if err != nil {
		return IssuedCredential{}, err
	}
	idle, max := now.Add(CredentialIdleLifetime), now.Add(CredentialMaxLifetime)
	result, err := tx.Exec(`INSERT INTO session_credentials(session_id,token_hash,capabilities,ceiling,created_at_ms,idle_expires_at_ms,max_expires_at_ms) VALUES(?,?,?,?,?,?,?)`,
		sessionID, HashSessionToken(token), string(encodedCaps), string(encodedCeiling), now.UnixMilli(), idle.UnixMilli(), max.UnixMilli())
	if err != nil {
		return IssuedCredential{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return IssuedCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{Token: token, Credential: SessionCredential{
		ID: id, SessionID: sessionID, Capabilities: capabilities, Ceiling: encodedCeiling,
		CreatedAt: time.UnixMilli(now.UnixMilli()), IdleExpiresAt: time.UnixMilli(idle.UnixMilli()), MaxExpiresAt: time.UnixMilli(max.UnixMilli()),
	}}, nil
}

func (r *Registry) AuthenticateSessionToken(token string, now time.Time) (SessionCredential, error) {
	if !strings.HasPrefix(token, SessionTokenPrefix) {
		return SessionCredential{}, ErrCredentialUnknown
	}
	tx, err := r.db.Begin()
	if err != nil {
		return SessionCredential{}, err
	}
	defer tx.Rollback()
	var hostID string
	var archived int
	row, err := scanCredential(tx.QueryRow(credentialSelect+` WHERE token_hash=?`, HashSessionToken(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionCredential{}, ErrCredentialUnknown
	}
	if err != nil {
		return SessionCredential{}, err
	}
	if row.SessionID == "" {
		return SessionCredential{}, ErrCredentialUnknown
	}
	if row.revokedAt != 0 {
		return SessionCredential{}, CredentialRevokedError{Reason: row.revokedReason}
	}
	credential := row.SessionCredential
	if err := tx.QueryRow(`SELECT s.host_id,EXISTS(SELECT 1 FROM session_archive a WHERE a.session_id=s.id) FROM sessions s WHERE s.id=?`, credential.SessionID).Scan(&hostID, &archived); err != nil {
		return SessionCredential{}, err
	}
	if ended := endedReason(hostID, archived); ended != "" {
		if _, err := tx.Exec(`UPDATE session_credentials SET revoked_at_ms=?,revoked_reason=? WHERE id=?`, now.UnixMilli(), ended, credential.ID); err != nil {
			return SessionCredential{}, err
		}
		if err := tx.Commit(); err != nil {
			return SessionCredential{}, err
		}
		return SessionCredential{}, CredentialRevokedError{Reason: ended}
	}
	if !credential.MaxExpiresAt.After(now) {
		return SessionCredential{}, ErrCredentialMaxExpired
	}
	if !credential.IdleExpiresAt.After(now) {
		return SessionCredential{}, ErrCredentialExpired
	}
	idle := now.Add(CredentialIdleLifetime)
	if _, err := tx.Exec(`UPDATE session_credentials SET last_used_at_ms=?,idle_expires_at_ms=? WHERE id=?`, now.UnixMilli(), idle.UnixMilli(), credential.ID); err != nil {
		return SessionCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionCredential{}, err
	}
	credential.LastUsedAt = time.UnixMilli(now.UnixMilli())
	credential.IdleExpiresAt = time.UnixMilli(idle.UnixMilli())
	return credential, nil
}

func (r *Registry) ReviveSessionCredential(sessionID string, now time.Time) error {
	_, err := r.db.Exec(`UPDATE session_credentials SET idle_expires_at_ms=? WHERE session_id=? AND revoked_at_ms=0 AND max_expires_at_ms>?`, now.Add(CredentialIdleLifetime).UnixMilli(), sessionID, now.UnixMilli())
	return err
}

func (r *Registry) RevokeSessionCredentials(sessionID, reason string, now time.Time) error {
	_, err := r.db.Exec(`UPDATE session_credentials SET revoked_at_ms=?,revoked_reason=? WHERE session_id=? AND revoked_at_ms=0`, now.UnixMilli(), reason, sessionID)
	return err
}

func endedReason(hostID string, archived int) string {
	switch {
	case archived != 0:
		return RevokedSessionArchive
	case hostID != "":
		return RevokedSessionMoved
	}
	return ""
}

const credentialSelect = `SELECT id,session_id,capabilities,ceiling,created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason FROM session_credentials`

type credentialRow struct {
	SessionCredential
	revokedAt     int64
	revokedReason string
}

func scanCredential(row *sql.Row) (credentialRow, error) {
	var result credentialRow
	var caps, ceiling string
	var sessionID sql.NullString
	var created, used, idle, max int64
	if err := row.Scan(&result.ID, &sessionID, &caps, &ceiling, &created, &used, &idle, &max, &result.revokedAt, &result.revokedReason); err != nil {
		return credentialRow{}, err
	}
	result.SessionID = sessionID.String
	if err := json.Unmarshal([]byte(caps), &result.Capabilities); err != nil {
		return credentialRow{}, fmt.Errorf("decode credential capabilities: %w", err)
	}
	result.Ceiling = json.RawMessage(ceiling)
	result.CreatedAt, result.IdleExpiresAt, result.MaxExpiresAt = time.UnixMilli(created), time.UnixMilli(idle), time.UnixMilli(max)
	if used > 0 {
		result.LastUsedAt = time.UnixMilli(used)
	}
	return result, nil
}

func canonicalCeiling(ceiling json.RawMessage) (json.RawMessage, error) {
	if len(ceiling) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value any
	if err := json.Unmarshal(ceiling, &value); err != nil {
		return nil, fmt.Errorf("decode credential ceiling: %w", err)
	}
	return json.Marshal(value)
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
