package registry

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
)

type PendingCredential struct {
	Token string
	Ref   string
}

// A Codex thread has no id until thread/start returns, yet its token must be in that call; the id is claimed after and binds at registration.
func (r *Registry) IssuePendingCredential(ceiling json.RawMessage, now time.Time, lifetime time.Duration) (PendingCredential, error) {
	token, err := newSessionToken()
	if err != nil {
		return PendingCredential{}, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return PendingCredential{}, err
	}
	ref := hex.EncodeToString(raw)
	tx, err := r.db.Begin()
	if err != nil {
		return PendingCredential{}, err
	}
	defer tx.Rollback()
	if err := prunePendingCredentialsTx(tx, now); err != nil {
		return PendingCredential{}, err
	}
	idle, max := now.Add(CredentialIdleLifetime), now.Add(CredentialMaxLifetime)
	result, err := tx.Exec(`INSERT INTO session_credentials(pending_ref,pending_expires_at_ms,token_hash,created_at_ms,idle_expires_at_ms,max_expires_at_ms) VALUES(?,?,?,?,?,?)`,
		ref, now.Add(lifetime).UnixMilli(), HashSessionToken(token), now.UnixMilli(), idle.UnixMilli(), max.UnixMilli())
	if err != nil {
		return PendingCredential{}, err
	}
	if bounded(ceiling) {
		id, err := result.LastInsertId()
		if err != nil {
			return PendingCredential{}, err
		}
		if _, err := tx.Exec(`INSERT INTO pending_ceilings(credential_id,ceiling) VALUES(?,?)`, id, string(ceiling)); err != nil {
			return PendingCredential{}, err
		}
	}
	return PendingCredential{Token: token, Ref: ref}, tx.Commit()
}

func (r *Registry) PendingCredentialLive(token string, now time.Time) (bool, error) {
	var live bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_credentials WHERE token_hash=? AND session_id IS NULL AND revoked_at_ms=0 AND pending_expires_at_ms>?)`, HashSessionToken(token), now.UnixMilli()).Scan(&live)
	return live, err
}

func (r *Registry) ClaimPendingCredential(ref, sessionID string, now time.Time) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE session_credentials SET claimed_session_id=? WHERE pending_ref=? AND session_id IS NULL AND claimed_session_id IS NULL AND pending_expires_at_ms>?`, sessionID, ref, now.UnixMilli())
	if err != nil {
		return false, err
	}
	if claimed, err := result.RowsAffected(); err != nil || claimed != 1 {
		return false, err
	}
	var registered int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, sessionID).Scan(&registered); err != nil {
		return false, err
	}
	if registered == 1 {
		if err := bindClaimedCredentialTx(tx, sessionID, now); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (r *Registry) PrunePendingCredentials(now time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := prunePendingCredentialsTx(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func prunePendingCredentialsTx(tx *sql.Tx, now time.Time) error {
	_, err := tx.Exec(`DELETE FROM session_credentials WHERE session_id IS NULL AND pending_expires_at_ms<=?`, now.UnixMilli())
	return err
}

func bindClaimedCredentialTx(tx *sql.Tx, sessionID string, now time.Time) error {
	var id int64
	err := tx.QueryRow(`SELECT id FROM session_credentials WHERE claimed_session_id=? AND session_id IS NULL AND pending_expires_at_ms>?`, sessionID, now.UnixMilli()).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE session_credentials SET revoked_at_ms=?,revoked_reason=? WHERE session_id=? AND revoked_at_ms=0`, now.UnixMilli(), RevokedRotated, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE session_credentials SET session_id=?,pending_ref=NULL,pending_expires_at_ms=0,claimed_session_id=NULL WHERE id=?`, sessionID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO session_ceilings(session_id,ceiling) SELECT ?,ceiling FROM pending_ceilings WHERE credential_id=? ON CONFLICT(session_id) DO NOTHING`, sessionID, id); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM pending_ceilings WHERE credential_id=?`, id)
	return err
}
