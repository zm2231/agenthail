package registry

import (
	"database/sql"
	"time"
)

func init() {
	registerMigration("2026-11-26-your-workers-stopped-mid-turn-close-their-turn", func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE turn_ends SET stopped_at_ms=? WHERE completed_turn_id='' AND stopped_at_ms=0
			AND EXISTS (SELECT 1 FROM sessions s WHERE s.id=turn_ends.session_id AND s.ended=1)
			AND started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends latest WHERE latest.session_id=turn_ends.session_id)`, time.Now().UnixMilli())
		return err
	})
}
