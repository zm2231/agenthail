package registry

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type Registry struct {
	db *sql.DB
}

func Open(path string) (*Registry, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".agenthail", "registry.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create registry directory: %w", err)
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", path+sep+"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	r := &Registry{db: db}
	if err := r.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Registry) Close() error { return r.db.Close() }

func (r *Registry) migrate() error {
	if _, err := r.db.Exec(schema); err != nil {
		return err
	}
	columns := []struct {
		name string
		decl string
	}{
		{"status", `TEXT NOT NULL DEFAULT 'pending'`},
		{"attempts", `INTEGER NOT NULL DEFAULT 0`},
		{"last_error", `TEXT NOT NULL DEFAULT ''`},
		{"available_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"inflight_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"delivery_key", `TEXT NOT NULL DEFAULT ''`},
		{"model", `TEXT NOT NULL DEFAULT ''`},
		{"updated_at", `TEXT NOT NULL DEFAULT ''`},
	}
	for _, column := range columns {
		if err := r.ensureColumn("message_queue", column.name, column.decl); err != nil {
			return err
		}
	}
	_, err := r.db.Exec(`
		UPDATE message_queue SET status=CASE WHEN delivered=1 THEN 'delivered' ELSE 'pending' END
		WHERE status='' OR (delivered=1 AND status!='delivered');
		CREATE UNIQUE INDEX IF NOT EXISTS message_queue_delivery_key
		ON message_queue(delivery_key) WHERE delivery_key!='';`)
	return err
}

func (r *Registry) ensureColumn(table, name, declaration string) error {
	rows, err := r.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var columnName, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if columnName == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = r.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + declaration)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '',
	has_local INTEGER NOT NULL DEFAULT 0,
	registered_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS aliases (
	name TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS channels (
	id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS channel_members (
	channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	PRIMARY KEY (channel_id, session_id)
);
CREATE TABLE IF NOT EXISTS routes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	from_session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	to_session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	channel_id TEXT REFERENCES channels(id) ON DELETE SET NULL,
	pattern TEXT NOT NULL DEFAULT '.*',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS message_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	message TEXT NOT NULL, queued_at TEXT NOT NULL DEFAULT (datetime('now')),
	delivered INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '', available_at_ms INTEGER NOT NULL DEFAULT 0,
	inflight_at_ms INTEGER NOT NULL DEFAULT 0, delivery_key TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS session_runtime (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	last_status TEXT NOT NULL DEFAULT 'unknown',
	active_turn_id TEXT NOT NULL DEFAULT '',
	completed_turn_id TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS relay_deliveries (
	route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
	completion_id TEXT NOT NULL,
	delivered_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (route_id, completion_id)
);
CREATE TABLE IF NOT EXISTS delivery_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	kind TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	source_session_id TEXT NOT NULL DEFAULT '',
	route_id INTEGER NOT NULL DEFAULT 0,
	queue_id INTEGER NOT NULL DEFAULT 0,
	completion_id TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS delivery_history_created_at ON delivery_history(created_at DESC, id DESC);
`

func (r *Registry) RegisterSession(s surface.Session) error {
	_, err := r.db.Exec(
		`INSERT INTO sessions (id,surface,name,cwd,pid,status,transcript,has_local,updated_at)
		 VALUES (?,?,?,?,?,?,?, ?,datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET surface=excluded.surface,name=excluded.name,cwd=excluded.cwd,
		   pid=excluded.pid,status=excluded.status,transcript=excluded.transcript,
		   has_local=excluded.has_local,updated_at=datetime('now')`,
		s.ID, string(s.Surface), s.Name, s.Cwd, s.PID, string(s.Status), s.Transcript, b2i(s.HasLocal))
	return err
}

func (r *Registry) LookupAlias(name string) (string, error) {
	var sid string
	err := r.db.QueryRow(`SELECT session_id FROM aliases WHERE name = ?`, name).Scan(&sid)
	return sid, err
}

func (r *Registry) SetAlias(name, sessionID string) error {
	_, err := r.db.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?) ON CONFLICT(name) DO UPDATE SET session_id=excluded.session_id`, name, sessionID)
	return err
}

func (r *Registry) ResolveTarget(target string) (string, error) {
	if sid, err := r.LookupAlias(target); err == nil {
		return sid, nil
	}
	var exact string
	if err := r.db.QueryRow(`SELECT id FROM sessions WHERE id = ?`, target).Scan(&exact); err == nil {
		return exact, nil
	}
	prefix := escapeLike(target) + "%"
	if ids, err := r.matchingIDs(`SELECT id FROM sessions WHERE id LIKE ? ESCAPE '\' ORDER BY updated_at DESC, id LIMIT 2`, prefix); err != nil {
		return "", err
	} else if len(ids) == 1 {
		return ids[0], nil
	} else if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous session id prefix %q matches %s", target, strings.Join(ids, ", "))
	}
	contains := "%" + escapeLike(target) + "%"
	ids, err := r.matchingIDs(`SELECT id FROM sessions WHERE name LIKE ? ESCAPE '\' OR cwd LIKE ? ESCAPE '\' ORDER BY updated_at DESC, id LIMIT 2`, contains, contains)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", sql.ErrNoRows
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous session target %q matches %s", target, strings.Join(ids, ", "))
	}
	return ids[0], nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (r *Registry) matchingIDs(query string, args ...any) ([]string, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Registry) CreateChannel(name string) (string, error) {
	id := fmt.Sprintf("ch_%d", time.Now().UnixNano())
	_, err := r.db.Exec(`INSERT INTO channels (id,name) VALUES (?,?)`, id, name)
	return id, err
}

func (r *Registry) AddToChannel(channelName, sessionID string) error {
	res, err := r.db.Exec(`INSERT OR IGNORE INTO channel_members (channel_id,session_id) SELECT id,? FROM channels WHERE name = ?`, sessionID, channelName)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		var exists int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE name=?`, channelName).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("channel %q does not exist", channelName)
		}
	}
	return nil
}

func (r *Registry) AddRoute(from, to, pattern string) (int64, error) {
	if _, err := regexp.Compile(pattern); err != nil {
		return 0, fmt.Errorf("invalid relay pattern: %w", err)
	}
	if from == to {
		return 0, fmt.Errorf("relay route cannot target its source")
	}
	var cycle int
	err := r.db.QueryRow(`
		WITH RECURSIVE reachable(id) AS (
			SELECT to_session FROM routes WHERE from_session=?
			UNION
			SELECT routes.to_session FROM routes JOIN reachable ON routes.from_session=reachable.id
		)
		SELECT COUNT(*) FROM reachable WHERE id=?`, to, from).Scan(&cycle)
	if err != nil {
		return 0, err
	}
	if cycle > 0 {
		return 0, fmt.Errorf("relay route would create a cycle")
	}
	res, err := r.db.Exec(`INSERT INTO routes (from_session,to_session,pattern) VALUES (?,?,?)`, from, to, pattern)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Registry) QueueMessage(sessionID, message string) error {
	_, err := r.QueueMessageWithKey(sessionID, message, "")
	return err
}

func (r *Registry) QueueMessageWithKey(sessionID, message, deliveryKey string) (int64, error) {
	return r.QueueMessageWithOptions(sessionID, message, deliveryKey, surface.SendOptions{})
}

func (r *Registry) QueueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions) (int64, error) {
	res, err := r.db.Exec(`INSERT INTO message_queue (session_id,message,delivery_key,model,status,updated_at) VALUES (?,?,?,?,'pending',datetime('now'))`, sessionID, message, deliveryKey, options.Model)
	if err != nil {
		if deliveryKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			var id int64
			lookupErr := r.db.QueryRow(`SELECT id FROM message_queue WHERE delivery_key=?`, deliveryKey).Scan(&id)
			return id, lookupErr
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	// History is observability, not delivery. Do not turn a successful enqueue
	// into a failed send if the audit database write is unavailable.
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: message, Result: options.Model})
	return id, nil
}

func (r *Registry) QueueCount(sessionID string) int {
	var n int
	r.db.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, sessionID).Scan(&n)
	return n
}

func (r *Registry) QueueCounts() (map[string]int, error) {
	rows, err := r.db.Query(`SELECT session_id,COUNT(*) FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var sessionID string
		var count int
		if err := rows.Scan(&sessionID, &count); err != nil {
			return nil, err
		}
		counts[sessionID] = count
	}
	return counts, rows.Err()
}

type QueuedMessage struct {
	ID        int64
	SessionID string
	Message   string
	Model     string
	Attempts  int
}

const (
	maxHistoryText = 16 * 1024
	maxHistoryRows = 2000
)

// HistoryEntry is the durable, operator-facing record of work moving through
// the daemon. It intentionally stores bounded message/result text so a single
// verbose agent cannot grow the registry without limit.
type HistoryEntry struct {
	ID              int64  `json:"id"`
	CreatedAt       string `json:"createdAt"`
	Kind            string `json:"kind"`
	SessionID       string `json:"sessionId,omitempty"`
	SourceSessionID string `json:"sourceSessionId,omitempty"`
	RouteID         int64  `json:"routeId,omitempty"`
	QueueID         int64  `json:"queueId,omitempty"`
	CompletionID    string `json:"completionId,omitempty"`
	Message         string `json:"message,omitempty"`
	Result          string `json:"result,omitempty"`
	Error           string `json:"error,omitempty"`
}

func boundHistoryText(value string) string {
	if len(value) <= maxHistoryText {
		return value
	}
	return value[:maxHistoryText] + "\n[truncated]"
}

func (r *Registry) RecordHistory(entry HistoryEntry) error {
	if entry.Kind == "" {
		return fmt.Errorf("history kind is required")
	}
	res, err := r.db.Exec(`INSERT INTO delivery_history(kind,session_id,source_session_id,route_id,queue_id,completion_id,message,result,error) VALUES(?,?,?,?,?,?,?,?,?)`,
		entry.Kind, entry.SessionID, entry.SourceSessionID, entry.RouteID, entry.QueueID, entry.CompletionID,
		boundHistoryText(entry.Message), boundHistoryText(entry.Result), boundHistoryText(entry.Error))
	if err != nil {
		return err
	}
	// Keep the audit trail useful on a long-running workstation without letting
	// it become an unbounded transcript store.
	if id, idErr := res.LastInsertId(); idErr == nil {
		_, _ = r.db.Exec(`DELETE FROM delivery_history WHERE id <= ?`, id-maxHistoryRows)
	}
	return nil
}

func (r *Registry) ListHistory(limit int, sessionID string) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `SELECT id,created_at,kind,session_id,source_session_id,route_id,queue_id,completion_id,message,result,error FROM delivery_history`
	args := []any{}
	if sessionID != "" {
		query += ` WHERE session_id=? OR source_session_id=?`
		args = append(args, sessionID, sessionID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]HistoryEntry, 0)
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.ID, &entry.CreatedAt, &entry.Kind, &entry.SessionID, &entry.SourceSessionID, &entry.RouteID, &entry.QueueID, &entry.CompletionID, &entry.Message, &entry.Result, &entry.Error); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

type QueueRow struct {
	ID        int64  `json:"id"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
	Model     string `json:"model,omitempty"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"lastError,omitempty"`
	QueuedAt  string `json:"queuedAt"`
}

const uncertainDeliveryError = "delivery outcome is unknown after daemon interruption; retry explicitly if the target did not receive it"

func (r *Registry) ListQueue(includeDelivered bool) ([]QueueRow, error) {
	query := `SELECT id,session_id,message,model,status,attempts,last_error,queued_at FROM message_queue`
	if !includeDelivered {
		query += ` WHERE status NOT IN ('delivered','canceled')`
	}
	query += ` ORDER BY id`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]QueueRow, 0)
	for rows.Next() {
		var row QueueRow
		if err := rows.Scan(&row.ID, &row.SessionID, &row.Message, &row.Model, &row.Status, &row.Attempts, &row.LastError, &row.QueuedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *Registry) RetryMessage(id int64) error {
	var sessionID, message string
	_ = r.db.QueryRow(`SELECT session_id,message FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message)
	res, err := r.db.Exec(`UPDATE message_queue SET status='pending',attempts=0,last_error='',available_at_ms=0,inflight_at_ms=0,delivered=0,updated_at=datetime('now') WHERE id=? AND status='dead'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not dead-lettered", id)
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "retry", SessionID: sessionID, QueueID: id, Message: message, Result: "scheduled"})
	return nil
}

func (r *Registry) CancelMessage(id int64) error {
	var sessionID, message string
	_ = r.db.QueryRow(`SELECT session_id,message FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message)
	res, err := r.db.Exec(`UPDATE message_queue SET status='canceled',updated_at=datetime('now') WHERE id=? AND status IN ('pending','dead')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not pending or dead-lettered", id)
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "removed from delivery queue"})
	return nil
}

func (r *Registry) CancelMessagesForSession(sessionID string) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,message FROM message_queue WHERE session_id=? AND status='pending'`, sessionID)
	if err != nil {
		return 0, err
	}
	var pending []HistoryEntry
	for rows.Next() {
		var id int64
		var message string
		if err := rows.Scan(&id, &message); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "removed from pending queue"})
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`UPDATE message_queue SET status='canceled',updated_at=datetime('now') WHERE session_id=? AND status='pending'`, sessionID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, entry := range pending {
		_ = r.RecordHistory(entry)
	}
	return res.RowsAffected()
}

func (r *Registry) ClaimNextMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var item QueuedMessage
	var status string
	var availableAt int64
	var inflightAt int64
	err = tx.QueryRow(`SELECT id,session_id,message,model,attempts,status,available_at_ms,inflight_at_ms FROM message_queue WHERE session_id=? AND status IN ('pending','inflight') ORDER BY id LIMIT 1`, sessionID).Scan(&item.ID, &item.SessionID, &item.Message, &item.Model, &item.Attempts, &status, &availableAt, &inflightAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status == "inflight" && inflightAt > 0 && inflightAt < now.Add(-time.Minute).UnixMilli() {
		if _, err := tx.Exec(`UPDATE message_queue SET status='dead',last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, uncertainDeliveryError, item.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if status != "pending" || availableAt > now.UnixMilli() {
		return nil, nil
	}
	res, err := tx.Exec(`UPDATE message_queue SET status='inflight',attempts=attempts+1,inflight_at_ms=?,updated_at=datetime('now') WHERE id=? AND status='pending'`, now.UnixMilli(), item.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, fmt.Errorf("queue item %d was claimed concurrently", item.ID)
	}
	item.Attempts++
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *Registry) AckMessage(id int64) error {
	res, err := r.db.Exec(`UPDATE message_queue SET status='delivered',delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	return nil
}

func (r *Registry) NackMessage(id int64, cause error, now time.Time, maxAttempts int) error {
	var attempts int
	if err := r.db.QueryRow(`SELECT attempts FROM message_queue WHERE id=?`, id).Scan(&attempts); err != nil {
		return err
	}
	status := "pending"
	available := now
	if attempts >= maxAttempts {
		status = "dead"
	} else {
		shift := attempts - 1
		if shift < 0 {
			shift = 0
		}
		if shift > 6 {
			shift = 6
		}
		available = now.Add(5 * time.Second * time.Duration(1<<shift))
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, err := r.db.Exec(`UPDATE message_queue SET status=?,last_error=?,available_at_ms=?,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, status, message, available.UnixMilli(), id)
	return err
}

func (r *Registry) DeadLetterUnknown(id int64, cause error) error {
	message := uncertainDeliveryError
	if cause != nil {
		message = fmt.Sprintf("delivery outcome is unknown: %s; retry explicitly if the target did not receive it", cause)
	}
	res, err := r.db.Exec(`UPDATE message_queue SET status='dead',last_error=?,available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, message, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	return nil
}

func (r *Registry) RecoverInflight(before time.Time) (int64, error) {
	res, err := r.db.Exec(`UPDATE message_queue SET status='dead',last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE status='inflight' AND inflight_at_ms<?`, uncertainDeliveryError, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type RuntimeState struct {
	LastStatus      surface.SessionStatus
	ActiveTurnID    string
	CompletedTurnID string
}

func (r *Registry) RuntimeState(sessionID string) (RuntimeState, bool, error) {
	var state RuntimeState
	var status string
	err := r.db.QueryRow(`SELECT last_status,active_turn_id,completed_turn_id FROM session_runtime WHERE session_id=?`, sessionID).Scan(&status, &state.ActiveTurnID, &state.CompletedTurnID)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.LastStatus = surface.SessionStatus(status)
	return state, true, nil
}

func (r *Registry) SaveRuntimeState(sessionID string, observation surface.TurnObservation) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,updated_at) VALUES(?,?,?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,completed_turn_id=excluded.completed_turn_id,updated_at=datetime('now')`, sessionID, string(observation.Status), observation.ActiveTurnID, observation.CompletedTurnID)
	return err
}

func (r *Registry) RecordRelayDelivery(routeID int64, completionID string) (bool, error) {
	res, err := r.db.Exec(`INSERT OR IGNORE INTO relay_deliveries(route_id,completion_id) VALUES(?,?)`, routeID, completionID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Registry) ForgetRelayDelivery(routeID int64, completionID string) error {
	_, err := r.db.Exec(`DELETE FROM relay_deliveries WHERE route_id=? AND completion_id=?`, routeID, completionID)
	return err
}

type WatchedSession struct {
	ID      string
	Surface surface.SurfaceKind
}

func (r *Registry) WatchedSessions() ([]WatchedSession, error) {
	rows, err := r.db.Query(`
		SELECT DISTINCT s.id,s.surface
		FROM sessions s
		WHERE s.id IN (
			SELECT from_session FROM routes
			UNION SELECT to_session FROM routes
			UNION SELECT session_id FROM message_queue WHERE status IN ('pending','inflight')
		)
		ORDER BY s.surface,s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []WatchedSession
	for rows.Next() {
		var session WatchedSession
		var kind string
		if err := rows.Scan(&session.ID, &kind); err != nil {
			return nil, err
		}
		session.Surface = surface.SurfaceKind(kind)
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (r *Registry) RemoveRoute(id int64) error {
	res, err := r.db.Exec(`DELETE FROM routes WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("relay route %d does not exist", id)
	}
	return nil
}

type RouteRow struct {
	ID          int64
	FromSession string
	ToSession   string
	Pattern     string
}

func (r *Registry) ListRoutes() ([]RouteRow, error) {
	rows, err := r.db.Query(`SELECT id, from_session, to_session, pattern FROM routes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteRow
	for rows.Next() {
		var r RouteRow
		if err := rows.Scan(&r.ID, &r.FromSession, &r.ToSession, &r.Pattern); err == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

type AliasRow struct {
	Name      string
	SessionID string
}

func (r *Registry) ListAliases() ([]AliasRow, error) {
	rows, err := r.db.Query(`SELECT name, session_id FROM aliases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AliasRow
	for rows.Next() {
		var a AliasRow
		if err := rows.Scan(&a.Name, &a.SessionID); err == nil {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

type ChannelRow struct {
	Name        string
	MemberCount int
	Members     []string
}

func (r *Registry) ListChannels() ([]ChannelRow, error) {
	rows, err := r.db.Query(`
		SELECT c.name, COALESCE(count(cm.session_id),0) as n
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id
		GROUP BY c.id
		ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelRow
	for rows.Next() {
		var ch ChannelRow
		if err := rows.Scan(&ch.Name, &ch.MemberCount); err == nil {
			out = append(out, ch)
		}
	}
	for i := range out {
		members, _ := r.ChannelMembers(out[i].Name)
		out[i].Members = members
	}
	return out, nil
}

func (r *Registry) ChannelMembers(channelName string) ([]string, error) {
	rows, err := r.db.Query(`
		SELECT cm.session_id FROM channel_members cm
		JOIN channels c ON c.id = cm.channel_id
		WHERE c.name = ?`, channelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *Registry) GetSession(id string) (surface, name, cwd string, err error) {
	err = r.db.QueryRow(`SELECT surface, name, cwd FROM sessions WHERE id = ?`, id).Scan(&surface, &name, &cwd)
	return
}

func (r *Registry) Session(id string) (*surface.Session, error) {
	var session surface.Session
	var kind, status string
	var hasLocal int
	err := r.db.QueryRow(`SELECT id,surface,name,cwd,pid,status,transcript,has_local FROM sessions WHERE id = ?`, id).Scan(
		&session.ID, &kind, &session.Name, &session.Cwd, &session.PID, &status, &session.Transcript, &hasLocal,
	)
	if err != nil {
		return nil, err
	}
	session.Surface = surface.SurfaceKind(kind)
	session.Status = surface.SessionStatus(status)
	session.HasLocal = hasLocal != 0
	return &session, nil
}

func (r *Registry) ReverseAlias(sessionID string) (string, error) {
	var name string
	err := r.db.QueryRow(`SELECT name FROM aliases WHERE session_id = ?`, sessionID).Scan(&name)
	return name, err
}

func (r *Registry) RemoveAlias(name string) error {
	res, err := r.db.Exec(`DELETE FROM aliases WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("alias %q does not exist", name)
	}
	return nil
}

func (r *Registry) RemoveFromChannel(channelName, sessionID string) error {
	res, err := r.db.Exec(`DELETE FROM channel_members WHERE channel_id = (SELECT id FROM channels WHERE name = ?) AND session_id = ?`, channelName, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("session %q is not a member of channel %q", sessionID, channelName)
	}
	return nil
}

func (r *Registry) DeleteChannel(channelName string) error {
	res, err := r.db.Exec(`DELETE FROM channels WHERE name = ?`, channelName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("channel %q does not exist", channelName)
	}
	return nil
}
