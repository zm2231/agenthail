package registry

import (
	"database/sql"

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
		`DELETE FROM message_queue WHERE session_id=?3`,
		`DELETE FROM sessions WHERE id=?3`,
	} {
		if _, err := tx.Exec(statement, surface.PersonSessionID, surface.CLISessionID, retiredOperatorID); err != nil {
			return err
		}
	}
	return nil
}
