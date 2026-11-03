package registry

import (
	"database/sql"
	"errors"
)

func setAsideBoundOnlyCredentials(tx *sql.Tx) error {
	exists, err := columnExists(tx, "session_credentials", "token_hash")
	if err != nil || !exists {
		return err
	}
	pending, err := columnExists(tx, "session_credentials", "pending_ref")
	if err != nil || pending {
		return err
	}
	for _, statement := range []string{
		`ALTER TABLE session_credentials RENAME TO session_credentials_bound_only`,
		`DROP INDEX IF EXISTS session_credentials_live`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func upgradeBoundOnlyCredentials(tx *sql.Tx) error {
	var name string
	err := tx.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='session_credentials_bound_only'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO session_credentials(id,session_id,token_hash,capabilities,ceiling,created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason)
		SELECT id,session_id,token_hash,capabilities,ceiling,created_at_ms,last_used_at_ms,idle_expires_at_ms,max_expires_at_ms,revoked_at_ms,revoked_reason FROM session_credentials_bound_only`); err != nil {
		return err
	}
	_, err = tx.Exec(`DROP TABLE session_credentials_bound_only`)
	return err
}
