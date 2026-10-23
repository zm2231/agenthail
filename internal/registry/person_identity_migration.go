package registry

import (
	"database/sql"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

const retiredOperatorID = "agenthail-operator"

// splitOperator is the schema step that retires the one operator identity
// every sourceless send used to share. A queued message the person wrote moves
// to the person with its intent and history; everything else the operator
// sent was the CLI's or cannot be told apart, so it moves to the Agenthail
// CLI. Intents sent directly never recorded an author and stay with the CLI.
func splitOperator(tx *sql.Tx) error {
	var present int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id=?`, retiredOperatorID).Scan(&present); err != nil {
		return err
	}
	if present > 0 {
		for _, id := range []string{surface.PersonSessionID, surface.CLISessionID} {
			sender, _ := surface.AgenthailSender(id, "")
			if _, err := tx.Exec(`INSERT OR IGNORE INTO sessions(id,surface,name,status,runtime_kind) VALUES(?,?,?,?,?)`, sender.ID, string(sender.Surface), sender.Name, string(sender.Status), string(sender.RuntimeKind)); err != nil {
				return err
			}
		}
	}
	const personQueue = `SELECT id FROM message_queue WHERE author='person' AND source_session_id IN ('', ?3)`
	for _, statement := range []string{
		`UPDATE delivery_intents SET sender_session_id=?1 WHERE sender_session_id=?3 AND queue_id IN (` + personQueue + `)`,
		`UPDATE delivery_history SET source_session_id=?1 WHERE source_session_id IN ('', ?3) AND queue_id IN (` + personQueue + `)`,
		`UPDATE message_queue SET source_session_id=?1 WHERE id IN (` + personQueue + `)`,
		`UPDATE delivery_intents SET sender_session_id=?2 WHERE sender_session_id=?3`,
		`UPDATE delivery_intents SET target_session_id=?2 WHERE target_session_id=?3`,
		`UPDATE delivery_history SET source_session_id=?2 WHERE source_session_id=?3`,
		`UPDATE message_queue SET source_session_id=?2 WHERE source_session_id=?3`,
		`UPDATE session_origins SET creator_session_id=?2 WHERE creator_session_id=?3`,
	} {
		if _, err := tx.Exec(statement, surface.PersonSessionID, surface.CLISessionID, retiredOperatorID); err != nil {
			return err
		}
	}
	return dropOperatorSession(tx)
}

// dropOperatorSession deletes every row that still names the operator through
// a foreign key, then the operator itself, so no cascade decides what goes.
// What is left belongs to the operator as a session (an alias, catalog,
// runtime or journal row, a channel membership, a notice queued to it) or is
// a route to or from it, which never fires because no model reads it.
func dropOperatorSession(tx *sql.Tx) error {
	references, err := sessionReferences(tx)
	if err != nil {
		return err
	}
	for _, ref := range references {
		if _, err := tx.Exec(`DELETE FROM "`+ref.table+`" WHERE "`+ref.column+`"=?`, retiredOperatorID); err != nil {
			return err
		}
	}
	for _, ref := range references {
		var left int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM "`+ref.table+`" WHERE "`+ref.column+`"=?`, retiredOperatorID).Scan(&left); err != nil {
			return err
		}
		if left != 0 {
			return fmt.Errorf("%s.%s still names %s", ref.table, ref.column, retiredOperatorID)
		}
	}
	_, err = tx.Exec(`DELETE FROM sessions WHERE id=?`, retiredOperatorID)
	return err
}

type sessionReference struct{ table, column string }

func sessionReferences(tx *sql.Tx) ([]sessionReference, error) {
	return queryRows(tx, nil, func(row rowScanner) (sessionReference, error) {
		var ref sessionReference
		err := row.Scan(&ref.table, &ref.column)
		return ref, err
	}, `SELECT m.name, f."from" FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f WHERE m.type='table' AND f."table"='sessions'`)
}
