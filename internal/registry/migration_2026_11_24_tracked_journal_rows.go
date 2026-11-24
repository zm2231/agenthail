package registry

import "database/sql"

func init() {
	registerMigration("2026-11-24-tracked-journal-rows", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE session_journal_state SET retained_rows=(SELECT COUNT(*) FROM session_journal WHERE session_journal.session_id=session_journal_state.session_id)`)
		return err
	})
}
