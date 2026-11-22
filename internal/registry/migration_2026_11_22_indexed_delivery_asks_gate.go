package registry

import "database/sql"

func init() {
	registerMigration("2026-11-22-indexed-delivery-asks-gate", func(*sql.Tx) error { return nil })
}
