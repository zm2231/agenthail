package registry

import "database/sql"

func init() {
	registerMigration("2026-11-21-event-subscriptions-never-expire", func(tx *sql.Tx) error {
		exists, err := columnExists(tx, "mcp_event_subscriptions", "refresh_before_ms")
		if err != nil || !exists {
			return err
		}
		_, err = tx.Exec(`ALTER TABLE mcp_event_subscriptions DROP COLUMN refresh_before_ms`)
		return err
	})
}
