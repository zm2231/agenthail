package ledger

import (
	"database/sql"
	"errors"
)

func stampLegacy(tx *sql.Tx, projectKey, self string) error {
	var seq int64
	var typ string
	var size int
	err := tx.QueryRow(`SELECT seq, type, length(payload) FROM ledger_events WHERE project_key=? AND length(payload)>? ORDER BY seq LIMIT 1`, projectKey, MaxEventBytes).Scan(&seq, &typ, &size)
	switch {
	case err == nil:
		return refuse("event %d (%s) in %s holds %d bytes, over the %d-byte limit one replay carries; it cannot sync, so %s stays local", seq, typ, projectKey, size, MaxEventBytes, projectKey)
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = tx.Exec(`UPDATE ledger_events SET actor_host_id=?, origin_seq=seq WHERE project_key=? AND origin_seq=0`, self, projectKey)
	return err
}
