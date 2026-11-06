package registry

import (
	"encoding/json"
)

// A mirrored row's queue count is its host's count plus the sends this daemon
// holds for it. Applying a mirror row writes the sum in its own transaction,
// so the sum only goes stale when the queue changes under a mirrored session,
// or when a session that already has queued sends becomes mirrored. These
// triggers mark such a session in the writer's own transaction, whichever
// process writes.
const hostQueueDirtySchema = `
DROP TRIGGER IF EXISTS message_queue_host_count_insert;
CREATE TRIGGER message_queue_host_count_insert AFTER INSERT ON message_queue
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE id=NEW.session_id AND host_id<>'';
END;
DROP TRIGGER IF EXISTS message_queue_host_count_update;
CREATE TRIGGER message_queue_host_count_update AFTER UPDATE OF status,session_id ON message_queue
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE id IN (OLD.session_id,NEW.session_id) AND host_id<>'';
END;
DROP TRIGGER IF EXISTS message_queue_host_count_delete;
CREATE TRIGGER message_queue_host_count_delete AFTER DELETE ON message_queue
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE id=OLD.session_id AND host_id<>'';
END;
DROP TRIGGER IF EXISTS sessions_host_count_insert;
CREATE TRIGGER sessions_host_count_insert AFTER INSERT ON sessions
WHEN NEW.host_id<>''
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) VALUES(NEW.id);
END;
DROP TRIGGER IF EXISTS sessions_host_count_mirrored;
CREATE TRIGGER sessions_host_count_mirrored AFTER UPDATE OF host_id ON sessions
WHEN NEW.host_id<>''
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) VALUES(NEW.id);
END;`

// RefreshHostQueueCounts rewrites the mirrored rows the queue changed under
// whose projected count no longer matches. It reads and clears the marks in
// one write transaction, so a concurrent mirror write is never overwritten
// with an older projection and no mark is lost.
func (r *Registry) RefreshHostQueueCounts() (int, error) {
	var dirty bool
	if err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM host_queue_dirty)`).Scan(&dirty); err != nil || !dirty {
		return 0, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT cs.session_id,cs.projection_fingerprint,cs.observed_at,cs.unavailable_reason,cs.host_queue_count+COALESCE(q.n,0)
		FROM host_queue_dirty d CROSS JOIN catalog_sessions cs ON cs.session_id=d.session_id JOIN sessions s ON s.id=cs.session_id
		LEFT JOIN (SELECT session_id,COUNT(*) AS n FROM message_queue WHERE status IN ('pending','inflight') AND session_id IN (SELECT session_id FROM host_queue_dirty) GROUP BY session_id) q ON q.session_id=cs.session_id
		WHERE s.host_id<>'' AND json_valid(cs.projection_fingerprint)
		AND COALESCE(json_extract(cs.projection_fingerprint,'$.queueCount'),-1) IS NOT cs.host_queue_count+COALESCE(q.n,0)`)
	if err != nil {
		return 0, err
	}
	type stale struct {
		id, fingerprint, observedAt, unavailable string
		want                                     int
	}
	candidates, err := appendRows(nil, rows, func(scanner rowScanner) (stale, error) {
		var row stale
		err := scanner.Scan(&row.id, &row.fingerprint, &row.observedAt, &row.unavailable, &row.want)
		return row, err
	})
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, row := range candidates {
		var projection map[string]any
		if err := json.Unmarshal([]byte(row.fingerprint), &projection); err != nil {
			continue
		}
		entityID, _ := projection["id"].(string)
		if entityID == "" {
			continue
		}
		projection["queueCount"] = row.want
		encoded, err := json.Marshal(projection)
		if err != nil {
			return 0, err
		}
		generation, err := nextProjectionGenerationTx(tx)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE catalog_sessions SET projection_fingerprint=?,projection_generation=? WHERE session_id=?`, string(encoded), generation, row.id); err != nil {
			return 0, err
		}
		observedAt, _ := parseTimestamp(row.observedAt)
		if _, _, err := appendHostUpsertTx(tx, entityID, projection, generation, observedAt, row.unavailable); err != nil {
			return 0, err
		}
		changed++
	}
	if _, err := tx.Exec(`DELETE FROM host_queue_dirty`); err != nil {
		return 0, err
	}
	return changed, tx.Commit()
}
