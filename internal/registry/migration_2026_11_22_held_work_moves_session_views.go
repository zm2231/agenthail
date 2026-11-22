package registry

import "database/sql"

func init() {
	registerMigration("2026-11-22-held-work-moves-session-views", func(*sql.Tx) error { return nil })
}
