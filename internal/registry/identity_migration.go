package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// rekeyClaudeConversations is schema step 14. Claude rows were keyed by the
// Remote Control id of the process that registered them; a session id is now
// the conversation id Claude names the transcript after. Each such row moves
// to its conversation id with everything that references it. When the
// conversation already has a row, that row's alias wins. Rows without a
// transcript name no conversation: they are deleted when their id appears
// nowhere else and kept unchanged otherwise, so no alias, queued work or
// history is left pointing at a missing session.
// session_rekeys records each move so clients can follow ids they still hold.
// An existing registry commits user_version 14 with the data.
func (r *Registry) rekeyClaudeConversations(version int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,transcript FROM sessions WHERE surface=? AND (id LIKE 'session\_%' ESCAPE '\' OR id LIKE 'cse\_%' ESCAPE '\') ORDER BY updated_at DESC,registered_at DESC,rowid DESC`, string(surface.KindClaude))
	if err != nil {
		return err
	}
	type move struct{ old, new string }
	var moves []move
	var orphans []string
	for rows.Next() {
		var id, transcript string
		if err := rows.Scan(&id, &transcript); err != nil {
			rows.Close()
			return err
		}
		conversation := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
		if transcript == "" || conversation == "" || conversation == id {
			orphans = append(orphans, id)
			continue
		}
		moves = append(moves, move{id, conversation})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tables, err := existingTables(tx)
	if err != nil {
		return err
	}
	replacements := make([]string, 0, 2*len(moves))
	for _, m := range moves {
		if err := rekeySessionTx(tx, tables, m.old, m.new); err != nil {
			return fmt.Errorf("rekey Claude session %s to %s: %w", m.old, m.new, err)
		}
		replacements = append(replacements, m.old, m.new)
	}
	for _, id := range orphans {
		referenced, err := sessionReferencedTx(tx, id)
		if err != nil {
			return err
		}
		if referenced {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE id=?`, id); err != nil {
			return err
		}
	}
	if len(replacements) > 0 {
		replacer := strings.NewReplacer(replacements...)
		for table, columns := range map[string][]string{
			"catalog_events":          {"dedupe_key", "entity_id", "payload"},
			"daemon_events":           {"dedupe_key", "entity_id", "payload"},
			"daemon_event_items":      {"dedupe_key"},
			"action_receipts":         {"receipt"},
			"api_action_reservations": {"receipt"},
		} {
			if tables[table] {
				if err := rewriteSerializedTx(tx, table, columns, replacer); err != nil {
					return err
				}
			}
		}
	}
	if tables["catalog_sessions"] {
		for _, m := range moves {
			if err := announceRekeyTx(tx, m.old, m.new); err != nil {
				return err
			}
		}
	}
	if version >= 1 {
		if _, err := tx.Exec(`PRAGMA user_version=14`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// sessionReferencedTx reports whether id appears in any column that can hold
// a session id, keyed or not: foreign keys, history and ancestry columns, and
// serialized events and receipts.
func sessionReferencedTx(tx *sql.Tx, id string) (bool, error) {
	rows, err := tx.Query(`SELECT m.name,p.name FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type='table' AND m.name NOT IN ('session_rekeys','sqlite_sequence') AND NOT (m.name='sessions' AND p.name='id') AND (p.name LIKE '%session%' OR p.name IN ('entity_id','forked_from','dedupe_key','payload','receipt'))`)
	if err != nil {
		return false, err
	}
	type reference struct{ table, column string }
	var references []reference
	for rows.Next() {
		var ref reference
		if err := rows.Scan(&ref.table, &ref.column); err != nil {
			rows.Close()
			return false, err
		}
		references = append(references, ref)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, ref := range references {
		var found bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+ref.table+` WHERE instr(CAST(`+ref.column+` AS TEXT),?)>0)`, id).Scan(&found); err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func existingTables(tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = true
	}
	return tables, rows.Err()
}

func rekeySessionTx(tx *sql.Tx, tables map[string]bool, oldID, newID string) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, newID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		columns, err := tableColumns(tx, "sessions")
		if err != nil {
			return err
		}
		selected := make([]string, len(columns))
		for i, column := range columns {
			switch column {
			case "id":
				selected[i] = "?"
			case "bridge":
				selected[i] = "''"
			default:
				selected[i] = column
			}
		}
		if _, err := tx.Exec(`INSERT INTO sessions(`+strings.Join(columns, ",")+`) SELECT `+strings.Join(selected, ",")+` FROM sessions WHERE id=?`, newID, oldID); err != nil {
			return err
		}
	} else if _, err := tx.Exec(`UPDATE sessions SET transcript=CASE WHEN transcript='' THEN (SELECT transcript FROM sessions WHERE id=?1) ELSE transcript END,cwd=CASE WHEN cwd='' THEN (SELECT cwd FROM sessions WHERE id=?1) ELSE cwd END,has_local=MAX(has_local,(SELECT has_local FROM sessions WHERE id=?1)) WHERE id=?2`, oldID, newID); err != nil {
		return err
	}
	var hasAlias, hasRuntime, hasJournal, hasCatalog bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM aliases WHERE session_id=?),EXISTS(SELECT 1 FROM session_runtime WHERE session_id=?),EXISTS(SELECT 1 FROM session_journal_state WHERE session_id=?) OR EXISTS(SELECT 1 FROM session_journal WHERE session_id=?)`, newID, newID, newID, newID).Scan(&hasAlias, &hasRuntime, &hasJournal); err != nil {
		return err
	}
	if tables["catalog_sessions"] {
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_sessions WHERE session_id=?)`, newID).Scan(&hasCatalog); err != nil {
			return err
		}
	}
	type statement struct {
		table, query string
		skip         bool
	}
	statements := []statement{
		{"aliases", `UPDATE aliases SET session_id=?2 WHERE session_id=?1`, hasAlias},
		{"channel_members", `INSERT OR IGNORE INTO channel_members(channel_id,session_id) SELECT channel_id,?2 FROM channel_members WHERE session_id=?1`, false},
		{"routes", `UPDATE routes SET from_session=?2 WHERE from_session=?1`, false},
		{"routes", `UPDATE routes SET to_session=?2 WHERE to_session=?1`, false},
		{"routes", `DELETE FROM routes WHERE from_session=to_session AND from_session=?2`, false},
		{"routes", `DELETE FROM routes WHERE id NOT IN (SELECT MIN(id) FROM routes GROUP BY from_session,to_session,IFNULL(channel_id,''),pattern,handoff,once_only,active,event_cursor,last_goal_key) AND (from_session=?2 OR to_session=?2)`, false},
		{"message_queue", `UPDATE message_queue SET session_id=?2 WHERE session_id=?1`, false},
		{"message_queue", `UPDATE message_queue SET source_session_id=?2 WHERE source_session_id=?1`, false},
		{"attention_items", `UPDATE attention_items SET session_id=?2 WHERE session_id=?1`, false},
		{"delivery_history", `UPDATE delivery_history SET session_id=?2 WHERE session_id=?1`, false},
		{"delivery_history", `UPDATE delivery_history SET source_session_id=?2 WHERE source_session_id=?1`, false},
		{"api_action_reservations", `UPDATE api_action_reservations SET session_id=?2 WHERE session_id=?1`, false},
		{"api_action_reservations", `UPDATE api_action_reservations SET source_session_id=?2 WHERE source_session_id=?1`, false},
		{"delivery_intents", `UPDATE delivery_intents SET sender_session_id=?2 WHERE sender_session_id=?1`, false},
		{"delivery_intents", `UPDATE OR IGNORE delivery_intents SET target_session_id=?2 WHERE target_session_id=?1`, false},
		{"session_runtime", `UPDATE session_runtime SET session_id=?2 WHERE session_id=?1`, hasRuntime},
		{"session_journal_state", `UPDATE session_journal_state SET session_id=?2 WHERE session_id=?1`, hasJournal},
		{"session_journal", `UPDATE session_journal SET session_id=?2 WHERE session_id=?1`, hasJournal},
		{"session_journal_bodies", `UPDATE session_journal_bodies SET session_id=?2 WHERE session_id=?1`, hasJournal},
		{"catalog_sessions", `UPDATE catalog_sessions SET session_id=?2,projection_fingerprint='' WHERE session_id=?1`, hasCatalog},
		{"sessions", `UPDATE sessions SET parent_session_id=?2 WHERE parent_session_id=?1`, false},
		{"sessions", `UPDATE sessions SET root_session_id=?2 WHERE root_session_id=?1`, false},
		{"sessions", `UPDATE sessions SET forked_from=?2 WHERE forked_from=?1`, false},
	}
	for _, s := range statements {
		if s.skip || !tables[s.table] {
			continue
		}
		if _, err := tx.Exec(s.query, oldID, newID); err != nil {
			return fmt.Errorf("%s: %w", s.table, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id=?`, oldID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO session_rekeys(old_id,new_id) VALUES(?,?) ON CONFLICT(old_id) DO UPDATE SET new_id=excluded.new_id`, oldID, newID)
	return err
}

func tableColumns(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func rewriteSerializedTx(tx *sql.Tx, table string, wanted []string, replacer *strings.Replacer) error {
	present, err := tableColumns(tx, table)
	if err != nil {
		return err
	}
	var columns, selected []string
	for _, column := range present {
		if slices.Contains(wanted, column) {
			columns = append(columns, column)
			selected = append(selected, `CAST(`+column+` AS TEXT)`)
		}
	}
	if len(columns) == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT rowid,` + strings.Join(selected, ",") + ` FROM ` + table)
	if err != nil {
		return err
	}
	type update struct {
		rowid  int64
		values []any
	}
	var updates []update
	for rows.Next() {
		var rowid int64
		values := make([]sql.NullString, len(columns))
		targets := []any{&rowid}
		for i := range values {
			targets = append(targets, &values[i])
		}
		if err := rows.Scan(targets...); err != nil {
			rows.Close()
			return err
		}
		changed := false
		next := make([]any, len(columns))
		for i, value := range values {
			replaced := replacer.Replace(value.String)
			changed = changed || replaced != value.String
			if columns[i] == "payload" || columns[i] == "receipt" {
				next[i] = []byte(replaced)
			} else {
				next[i] = replaced
			}
		}
		if changed {
			updates = append(updates, update{rowid, next})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	assignments := make([]string, len(columns))
	for i, column := range columns {
		assignments[i] = column + `=?`
	}
	for _, u := range updates {
		if _, err := tx.Exec(`UPDATE OR REPLACE `+table+` SET `+strings.Join(assignments, ",")+` WHERE rowid=?`, append(u.values, u.rowid)...); err != nil {
			return err
		}
	}
	return nil
}

func announceRekeyTx(tx *sql.Tx, oldID, newID string) error {
	payload, err := json.Marshal(map[string]string{"sessionId": oldID, "surface": string(surface.KindClaude), "reason": "rekeyed", "rekeyedTo": newID})
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + oldID + ":rekeyed", Type: "session.removed", EntityID: oldID, Payload: payload})
	return err
}

// SessionRekeys maps each id retired by a rekey to the id the session
// continues under.
func (r *Registry) SessionRekeys() (map[string]string, error) {
	rows, err := r.read.Query(`SELECT old_id,new_id FROM session_rekeys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rekeys := map[string]string{}
	for rows.Next() {
		var oldID, newID string
		if err := rows.Scan(&oldID, &newID); err != nil {
			return nil, err
		}
		rekeys[oldID] = newID
	}
	return rekeys, rows.Err()
}
