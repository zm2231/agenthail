package registry

import "fmt"

func (r *Registry) AdvanceSessionTranscript(sessionID, previousPath, nextPath, identity string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE sessions SET transcript=? WHERE id=? AND transcript IN (?,?)", nextPath, sessionID, previousPath, nextPath)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("session transcript changed while advancing continuation")
	}
	if _, err := tx.Exec("UPDATE session_journal_state SET seed_identity=? WHERE session_id=?", identity, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}
