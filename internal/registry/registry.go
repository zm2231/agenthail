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
	db, err := sql.Open("sqlite", path+sep+"_pragma=foreign_keys(1)&_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
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
		{"relay_hops", `INTEGER NOT NULL DEFAULT 0`},
		{"expires_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"updated_at", `TEXT NOT NULL DEFAULT ''`},
	}
	for _, column := range columns {
		if err := r.ensureColumn("message_queue", column.name, column.decl); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name string
		decl string
	}{
		{"source", `TEXT NOT NULL DEFAULT ''`},
		{"transport", `TEXT NOT NULL DEFAULT ''`},
		{"last_active_ms", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := r.ensureColumn("sessions", column.name, column.decl); err != nil {
			return err
		}
	}
	if err := r.ensureColumn("session_runtime", "relay_hops", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	_, err := r.db.Exec(`
		UPDATE message_queue SET status=CASE WHEN delivered=1 THEN 'delivered' ELSE 'pending' END
		WHERE status='' OR (delivered=1 AND status!='delivered');
		UPDATE message_queue SET expires_at_ms=(CAST(strftime('%s',queued_at) AS INTEGER)*1000)+3600000
		WHERE expires_at_ms=0 AND status='pending';
		DELETE FROM aliases WHERE rowid NOT IN (SELECT MAX(rowid) FROM aliases GROUP BY session_id);
		CREATE UNIQUE INDEX IF NOT EXISTS message_queue_delivery_key
		ON message_queue(delivery_key) WHERE delivery_key!='';
		CREATE UNIQUE INDEX IF NOT EXISTS aliases_session_id ON aliases(session_id);`)
	if err != nil {
		return err
	}
	return r.mergeDuplicateClaudeSessions()
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
	source TEXT NOT NULL DEFAULT '', transport TEXT NOT NULL DEFAULT '',
	last_active_ms INTEGER NOT NULL DEFAULT 0,
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
	model TEXT NOT NULL DEFAULT '', relay_hops INTEGER NOT NULL DEFAULT 0,
	expires_at_ms INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS session_runtime (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	last_status TEXT NOT NULL DEFAULT 'unknown',
	active_turn_id TEXT NOT NULL DEFAULT '',
	completed_turn_id TEXT NOT NULL DEFAULT '',
	relay_hops INTEGER NOT NULL DEFAULT 0,
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
CREATE TABLE IF NOT EXISTS attention_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	queue_id INTEGER NOT NULL UNIQUE REFERENCES message_queue(id) ON DELETE CASCADE,
	reason TEXT NOT NULL,
	requested_action TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	resolved_at TEXT NOT NULL DEFAULT '',
	resolution TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS attention_items_open ON attention_items(resolved_at, created_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS device_pairings (
	id TEXT PRIMARY KEY,
	secret_hash TEXT NOT NULL UNIQUE,
	requested_name TEXT NOT NULL DEFAULT '',
	scopes TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS device_pairings_expires ON device_pairings(expires_at);
CREATE TABLE IF NOT EXISTS paired_devices (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	scopes TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	last_seen_at TEXT NOT NULL DEFAULT '',
	revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS paired_devices_active ON paired_devices(revoked_at, created_at DESC);
CREATE TABLE IF NOT EXISTS device_push_targets (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	installation_id TEXT NOT NULL,
	credential TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS daemon_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL DEFAULT '',
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS daemon_events_created ON daemon_events(created_at DESC, id DESC);
`

func (r *Registry) RegisterSession(s surface.Session) error {
	lastActiveMS := int64(0)
	if !s.LastActive.IsZero() {
		lastActiveMS = s.LastActive.UnixMilli()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		`INSERT INTO sessions (id,surface,name,cwd,pid,status,transcript,has_local,source,transport,last_active_ms,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET surface=excluded.surface,name=excluded.name,cwd=excluded.cwd,
		   pid=excluded.pid,status=excluded.status,transcript=excluded.transcript,
		   has_local=excluded.has_local,
		   source=CASE WHEN sessions.surface='codex' AND sessions.transport='managed' THEN sessions.source ELSE excluded.source END,
		   transport=CASE WHEN sessions.surface='codex' AND sessions.transport='managed' THEN sessions.transport ELSE excluded.transport END,
		   last_active_ms=excluded.last_active_ms,
		   updated_at=datetime('now')`,
		s.ID, string(s.Surface), s.Name, s.Cwd, s.PID, string(s.Status), s.Transcript, b2i(s.HasLocal), s.Source, s.Transport, lastActiveMS)
	if err != nil {
		return err
	}
	if s.Surface == surface.KindClaude && s.Transcript != "" {
		rows, err := tx.Query(`SELECT id FROM sessions WHERE surface=? AND transcript=? AND id<>?`, string(surface.KindClaude), s.Transcript, s.ID)
		if err != nil {
			return err
		}
		var duplicates []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			duplicates = append(duplicates, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, duplicate := range duplicates {
			if err := mergeSessionTx(tx, duplicate, s.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (r *Registry) mergeDuplicateClaudeSessions() error {
	rows, err := r.db.Query(`SELECT transcript FROM sessions WHERE surface=? AND transcript<>'' GROUP BY transcript HAVING COUNT(*)>1`, string(surface.KindClaude))
	if err != nil {
		return err
	}
	var transcripts []string
	for rows.Next() {
		var transcript string
		if err := rows.Scan(&transcript); err != nil {
			rows.Close()
			return err
		}
		transcripts = append(transcripts, transcript)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, transcript := range transcripts {
		ids, err := r.matchingIDs(`SELECT id FROM sessions WHERE surface=? AND transcript=? ORDER BY updated_at DESC,registered_at DESC,rowid DESC`, string(surface.KindClaude), transcript)
		if err != nil {
			return err
		}
		if len(ids) < 2 {
			continue
		}
		tx, err := r.db.Begin()
		if err != nil {
			return err
		}
		for _, duplicate := range ids[1:] {
			if err := mergeSessionTx(tx, duplicate, ids[0]); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func mergeSessionTx(tx *sql.Tx, oldID, currentID string) error {
	if oldID == currentID {
		return nil
	}
	var alias string
	_ = tx.QueryRow(`SELECT name FROM aliases WHERE session_id IN (?,?) ORDER BY rowid DESC LIMIT 1`, oldID, currentID).Scan(&alias)
	if _, err := tx.Exec(`INSERT OR IGNORE INTO channel_members(channel_id,session_id) SELECT channel_id,? FROM channel_members WHERE session_id=?`, currentID, oldID); err != nil {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM channel_members WHERE session_id=?`, []any{oldID}},
		{`UPDATE routes SET from_session=? WHERE from_session=?`, []any{currentID, oldID}},
		{`UPDATE routes SET to_session=? WHERE to_session=?`, []any{currentID, oldID}},
		{`DELETE FROM routes WHERE from_session=to_session`, nil},
		{`DELETE FROM routes WHERE id NOT IN (SELECT MIN(id) FROM routes GROUP BY from_session,to_session,IFNULL(channel_id,''),pattern) AND (from_session=? OR to_session=?)`, []any{currentID, currentID}},
		{`UPDATE message_queue SET session_id=? WHERE session_id=?`, []any{currentID, oldID}},
		{`UPDATE attention_items SET session_id=? WHERE session_id=?`, []any{currentID, oldID}},
		{`UPDATE delivery_history SET session_id=? WHERE session_id=?`, []any{currentID, oldID}},
		{`UPDATE delivery_history SET source_session_id=? WHERE source_session_id=?`, []any{currentID, oldID}},
		{`DELETE FROM session_runtime WHERE session_id=?`, []any{oldID}},
		{`DELETE FROM aliases WHERE session_id IN (?,?)`, []any{oldID, currentID}},
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement.query, statement.args...); err != nil {
			return err
		}
	}
	if alias != "" {
		if _, err := tx.Exec(`INSERT INTO aliases(name,session_id) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET session_id=excluded.session_id`, alias, currentID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`DELETE FROM sessions WHERE id=?`, oldID)
	return err
}

func (r *Registry) LookupAlias(name string) (string, error) {
	var sid string
	err := r.db.QueryRow(`SELECT session_id FROM aliases WHERE name = ?`, name).Scan(&sid)
	return sid, err
}

func (r *Registry) SetAlias(name, sessionID string) error {
	return r.ReplaceAlias(name, sessionID)
}

func (r *Registry) ReplaceAlias(name, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM aliases WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?) ON CONFLICT(name) DO UPDATE SET session_id=excluded.session_id`, name, sessionID); err != nil {
		return err
	}
	return tx.Commit()
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
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var cycle int
	err = tx.QueryRow(`
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
	res, err := tx.Exec(`INSERT INTO routes (from_session,to_session,pattern) VALUES (?,?,?)`, from, to, pattern)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (r *Registry) QueueMessage(sessionID, message string) error {
	_, err := r.QueueMessageWithKey(sessionID, message, "")
	return err
}

func (r *Registry) QueueMessageWithKey(sessionID, message, deliveryKey string) (int64, error) {
	return r.QueueMessageWithOptions(sessionID, message, deliveryKey, surface.SendOptions{})
}

func (r *Registry) QueueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions) (int64, error) {
	return r.queueMessageWithOptions(sessionID, message, deliveryKey, options, 0)
}

func (r *Registry) QueueRelayMessage(sessionID, message, deliveryKey string, relayHops int) (int64, error) {
	return r.queueMessageWithOptions(sessionID, message, deliveryKey, surface.SendOptions{}, relayHops)
}

func (r *Registry) queueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int) (int64, error) {
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	res, err := r.db.Exec(`INSERT INTO message_queue (session_id,message,delivery_key,model,relay_hops,expires_at_ms,status,updated_at) VALUES (?,?,?,?,?,?,'pending',datetime('now'))`, sessionID, message, deliveryKey, options.Model, relayHops, expiresAt)
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

func (r *Registry) expireMessages(now time.Time) error {
	_, err := r.ExpireMessages(now)
	return err
}

func (r *Registry) ExpireMessages(now time.Time) (int, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,session_id,message FROM message_queue WHERE status='pending' AND expires_at_ms>0 AND expires_at_ms<=?`, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	var expired []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.QueueID, &entry.SessionID, &entry.Message); err != nil {
			rows.Close()
			return 0, err
		}
		entry.Kind = "expired"
		entry.Result = "removed after 1 hour without delivery"
		expired = append(expired, entry)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE message_queue SET status='expired',last_error='message expired after 1 hour',updated_at=datetime('now') WHERE status='pending' AND expires_at_ms>0 AND expires_at_ms<=?`, now.UnixMilli()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, entry := range expired {
		_ = r.RecordHistory(entry)
	}
	return len(expired), nil
}

func (r *Registry) QueueCount(sessionID string) int {
	_ = r.expireMessages(time.Now())
	var n int
	r.db.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, sessionID).Scan(&n)
	return n
}

func (r *Registry) QueueCounts() (map[string]int, error) {
	if err := r.expireMessages(time.Now()); err != nil {
		return nil, err
	}
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
	RelayHops int
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

func (r *Registry) ListHistoryPage(limit int, beforeID int64, kind, queryText string) ([]HistoryEntry, bool, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	query := `SELECT h.id,h.created_at,h.kind,h.session_id,h.source_session_id,h.route_id,h.queue_id,h.completion_id,h.message,h.result,h.error FROM delivery_history h`
	args := []any{}
	conditions := []string{}
	if beforeID > 0 {
		conditions = append(conditions, `h.id < ?`)
		args = append(args, beforeID)
	}
	if kind != "" {
		conditions = append(conditions, `h.kind = ?`)
		args = append(args, kind)
	}
	if queryText != "" {
		conditions = append(conditions, `(h.kind LIKE ? ESCAPE '\' OR h.session_id LIKE ? ESCAPE '\' OR h.source_session_id LIKE ? ESCAPE '\' OR h.message LIKE ? ESCAPE '\' OR h.result LIKE ? ESCAPE '\' OR h.error LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM aliases a WHERE (a.session_id=h.session_id OR a.session_id=h.source_session_id) AND ('@' || a.name) LIKE ? ESCAPE '\') OR EXISTS (SELECT 1 FROM sessions s WHERE (s.id=h.session_id OR s.id=h.source_session_id) AND (s.surface || '/' || s.name) LIKE ? ESCAPE '\'))`)
		pattern := "%" + escapeLike(queryText) + "%"
		for range 8 {
			args = append(args, pattern)
		}
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY h.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	entries := make([]HistoryEntry, 0, limit+1)
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.ID, &entry.CreatedAt, &entry.Kind, &entry.SessionID, &entry.SourceSessionID, &entry.RouteID, &entry.QueueID, &entry.CompletionID, &entry.Message, &entry.Result, &entry.Error); err != nil {
			return nil, false, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}

func (r *Registry) ListHistoryKinds() ([]string, error) {
	rows, err := r.db.Query(`SELECT DISTINCT kind FROM delivery_history ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kinds := []string{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		kinds = append(kinds, kind)
	}
	return kinds, rows.Err()
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
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

type AttentionItem struct {
	ID              int64  `json:"id"`
	SessionID       string `json:"sessionId"`
	QueueID         int64  `json:"queueId"`
	Reason          string `json:"reason"`
	RequestedAction string `json:"requestedAction"`
	CreatedAt       string `json:"createdAt"`
	ResolvedAt      string `json:"resolvedAt,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
}

const uncertainDeliveryError = "delivery outcome is unknown after daemon interruption; retry explicitly if the target did not receive it"

func (r *Registry) ListQueue(includeDelivered bool) ([]QueueRow, error) {
	if err := r.expireMessages(time.Now()); err != nil {
		return nil, err
	}
	query := `SELECT id,session_id,message,model,status,attempts,last_error,queued_at,expires_at_ms FROM message_queue`
	if !includeDelivered {
		query += ` WHERE status NOT IN ('delivered','canceled','expired')`
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
		if err := rows.Scan(&row.ID, &row.SessionID, &row.Message, &row.Model, &row.Status, &row.Attempts, &row.LastError, &row.QueuedAt, &row.ExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *Registry) QueueItem(id int64) (*QueueRow, error) {
	if err := r.expireMessages(time.Now()); err != nil {
		return nil, err
	}
	var row QueueRow
	err := r.db.QueryRow(`SELECT id,session_id,message,model,status,attempts,last_error,queued_at,expires_at_ms FROM message_queue WHERE id=?`, id).Scan(&row.ID, &row.SessionID, &row.Message, &row.Model, &row.Status, &row.Attempts, &row.LastError, &row.QueuedAt, &row.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Registry) ListAttentionItems(includeResolved bool) ([]AttentionItem, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action)
		SELECT session_id,id,
			CASE WHEN last_error LIKE 'delivery outcome is unknown%' THEN 'Delivery outcome could not be confirmed' ELSE 'Delivery failed and needs a decision' END,
			'Retry or cancel this message'
		FROM message_queue WHERE status='dead'
		ON CONFLICT(queue_id) DO NOTHING`)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`UPDATE attention_items SET
		resolved_at=datetime('now'),
		resolution=COALESCE((SELECT CASE status WHEN 'pending' THEN 'retrying' WHEN 'delivered' THEN 'delivered' WHEN 'canceled' THEN 'canceled' ELSE status END FROM message_queue WHERE id=attention_items.queue_id),'removed')
		WHERE resolved_at='' AND NOT EXISTS (SELECT 1 FROM message_queue WHERE id=attention_items.queue_id AND status='dead')`)
	if err != nil {
		return nil, err
	}
	query := `SELECT id,session_id,queue_id,reason,requested_action,created_at,resolved_at,resolution FROM attention_items`
	if !includeResolved {
		query += ` WHERE resolved_at=''`
	}
	query += ` ORDER BY created_at DESC,id DESC`
	rows, err := tx.Query(query)
	if err != nil {
		return nil, err
	}
	items := make([]AttentionItem, 0)
	for rows.Next() {
		var item AttentionItem
		if err := rows.Scan(&item.ID, &item.SessionID, &item.QueueID, &item.Reason, &item.RequestedAction, &item.CreatedAt, &item.ResolvedAt, &item.Resolution); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Registry) RetryMessage(id int64) error {
	var sessionID, message string
	_ = r.db.QueryRow(`SELECT session_id,message FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message)
	res, err := r.db.Exec(`UPDATE message_queue SET status='pending',attempts=0,last_error='',available_at_ms=0,inflight_at_ms=0,expires_at_ms=?,delivered=0,updated_at=datetime('now') WHERE id=? AND status IN ('dead','expired')`, time.Now().Add(time.Hour).UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not dead-lettered or expired", id)
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
	if err := r.expireMessages(now); err != nil {
		return nil, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var item QueuedMessage
	var status string
	var availableAt int64
	var inflightAt int64
	err = tx.QueryRow(`SELECT id,session_id,message,model,attempts,relay_hops,status,available_at_ms,inflight_at_ms FROM message_queue WHERE session_id=? AND status IN ('pending','inflight') ORDER BY id LIMIT 1`, sessionID).Scan(&item.ID, &item.SessionID, &item.Message, &item.Model, &item.Attempts, &item.RelayHops, &status, &availableAt, &inflightAt)
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

func (r *Registry) AckMessageWithRelayHops(id int64, sessionID string, relayHops int) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET status='delivered',delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND session_id=? AND status='inflight'`, id, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,relay_hops,updated_at) VALUES(?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET relay_hops=excluded.relay_hops,updated_at=datetime('now')`, sessionID, relayHops); err != nil {
		return err
	}
	return tx.Commit()
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
	RelayHops       int
}

func (r *Registry) RuntimeState(sessionID string) (RuntimeState, bool, error) {
	var state RuntimeState
	var status string
	err := r.db.QueryRow(`SELECT last_status,active_turn_id,completed_turn_id,relay_hops FROM session_runtime WHERE session_id=?`, sessionID).Scan(&status, &state.ActiveTurnID, &state.CompletedTurnID, &state.RelayHops)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.LastStatus = surface.SessionStatus(status)
	return state, true, nil
}

func (r *Registry) MarkDeliveryStarted(sessionID, activeTurnID, completedTurnID string) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,updated_at) VALUES(?,?,?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,updated_at=datetime('now')`, sessionID, string(surface.StatusBusy), activeTurnID, completedTurnID)
	return err
}

func (r *Registry) SaveRuntimeState(sessionID string, observation surface.TurnObservation) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,updated_at) VALUES(?,?,?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,completed_turn_id=excluded.completed_turn_id,relay_hops=CASE WHEN excluded.completed_turn_id != session_runtime.completed_turn_id THEN 0 ELSE session_runtime.relay_hops END,updated_at=datetime('now')`, sessionID, string(observation.Status), observation.ActiveTurnID, observation.CompletedTurnID)
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
	if err := r.expireMessages(time.Now()); err != nil {
		return nil, err
	}
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
	var lastActiveMS int64
	err := r.db.QueryRow(`SELECT id,surface,name,cwd,pid,status,transcript,has_local,source,transport,last_active_ms FROM sessions WHERE id = ?`, id).Scan(
		&session.ID, &kind, &session.Name, &session.Cwd, &session.PID, &status, &session.Transcript, &hasLocal, &session.Source, &session.Transport, &lastActiveMS,
	)
	if err != nil {
		return nil, err
	}
	session.Surface = surface.SurfaceKind(kind)
	session.Status = surface.SessionStatus(status)
	session.HasLocal = hasLocal != 0
	if lastActiveMS > 0 {
		session.LastActive = time.UnixMilli(lastActiveMS)
	}
	return &session, nil
}

func (r *Registry) SessionUpdatedBefore(id string, before time.Time) (bool, error) {
	var stale bool
	err := r.db.QueryRow(`SELECT updated_at < datetime(?,'unixepoch') FROM sessions WHERE id=?`, before.Unix(), id).Scan(&stale)
	return stale, err
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
