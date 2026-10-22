package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

// Registry serializes writes through one connection and serves reads from a
// separate pool. In WAL mode readers never wait for the write lock, so a write
// blocked on another process's lock does not stall API reads.
type Registry struct {
	db         *sql.DB
	read       *sql.DB
	path       string
	now        func() time.Time
	busyPolicy func() (deliverypolicy.Mode, error)
}

// SetBusyPolicy sets the live busy-delivery policy. A queued message records
// only a per-message choice; one without a choice follows this policy when it
// is drained. Without a policy it queues.
func (r *Registry) SetBusyPolicy(policy func() (deliverypolicy.Mode, error)) {
	r.busyPolicy = policy
}

func (r *Registry) busyMode() (deliverypolicy.Mode, error) {
	if r.busyPolicy == nil {
		return deliverypolicy.Queue, nil
	}
	return r.busyPolicy()
}

// Steers reports whether a queued message with this per-message choice is a
// steer into a busy turn under the live policy.
func (r *Registry) Steers(busyDelivery string) (bool, error) {
	if busyDelivery != "" {
		return busyDelivery == string(deliverypolicy.Steer), nil
	}
	mode, err := r.busyMode()
	return mode == deliverypolicy.Steer, err
}

var generatedAliasCharacters = regexp.MustCompile(`[^a-z0-9._-]+`)

const (
	schemaVersion   = 33
	readConnections = 4
	// deadReviewWindow is how long a failed or uncertain delivery stays a
	// current decision before it moves to history. Pending work never ages out.
	deadReviewWindow = time.Hour
)

// SchemaVersion is the user_version a registry has once this build opens it.
func SchemaVersion() int { return schemaVersion }

// NewerSchemaError is returned by Open for a registry a newer agenthail has
// migrated. This build does not know that schema, so it refuses to read or
// write it rather than run against tables it would misuse.
type NewerSchemaError struct {
	Found     int
	Supported int
}

func (e *NewerSchemaError) Error() string {
	return fmt.Sprintf("registry is schema %d; this agenthail supports %d; install a newer agenthail", e.Found, e.Supported)
}

func defaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "registry.db")
}

func Open(path string) (*Registry, error) {
	private := path == ""
	if private {
		path = defaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create registry directory: %w", err)
	}
	if private {
		if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
			return nil, fmt.Errorf("restrict registry directory: %w", err)
		}
	}
	r, err := connect(path)
	if err != nil {
		return nil, err
	}
	if err := r.migrate(); err != nil {
		r.Close()
		return nil, err
	}
	// WAL mode is a property of the file, so it is set only once migrate has
	// accepted the schema; a registry this build refuses keeps its journal mode.
	if _, err := r.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		r.Close()
		return nil, fmt.Errorf("enable registry WAL: %w", err)
	}
	return r, nil
}

// OpenCurrent opens an existing registry only when it already has this
// build's schema, and changes nothing to get there: no directory, file,
// migration or journal mode. Short-lived processes another program starts,
// such as hooks, use it so an older or newer agenthail on the same machine
// never has its registry migrated under it.
func OpenCurrent(path string) (*Registry, error) {
	if path == "" {
		path = defaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	r, err := connect(path)
	if err != nil {
		return nil, err
	}
	var version int
	if err := r.read.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		r.Close()
		return nil, err
	}
	if version != schemaVersion {
		r.Close()
		return nil, fmt.Errorf("registry is schema %d; this agenthail uses %d", version, schemaVersion)
	}
	return r, nil
}

func connect(path string) (*Registry, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", path+sep+"_pragma=foreign_keys(1)&_pragma=busy_timeout(15000)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	read, err := sql.Open("sqlite", path+sep+"_pragma=busy_timeout(15000)&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	read.SetMaxOpenConns(readConnections)
	read.SetMaxIdleConns(readConnections)
	return &Registry{db: db, read: read, path: path, now: time.Now}, nil
}

func (r *Registry) Path() string { return r.path }

func (r *Registry) Close() error { return errors.Join(r.read.Close(), r.db.Close()) }

// migrate brings the database to the current schema in one transaction, so an
// interrupted upgrade leaves the previous schema intact.
func (r *Registry) migrate() error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := migrateTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateTx(tx *sql.Tx) error {
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return &NewerSchemaError{Found: version, Supported: schemaVersion}
	}
	if err := setAsideReleasedProjectsTable(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if err := upgradeReleasedProjectsTable(tx); err != nil {
		return err
	}
	if err := ensureHostIdentity(tx); err != nil {
		return err
	}
	if err := ensureColumn(tx, "routes", "once_only", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "routes", "active", `INTEGER NOT NULL DEFAULT 1`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "derived_version", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	for _, column := range []struct {
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
		{"source_session_id", `TEXT NOT NULL DEFAULT ''`},
		{"turn_options", `TEXT NOT NULL DEFAULT '{}'`},
		{"relay_hops", `INTEGER NOT NULL DEFAULT 0`},
		{"dead_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"deferrals", `INTEGER NOT NULL DEFAULT 0`},
		{"updated_at", `TEXT NOT NULL DEFAULT ''`},
		{"evidence", `TEXT NOT NULL DEFAULT ''`},
		{"operation", `TEXT NOT NULL DEFAULT 'message'`},
		{"busy_delivery", `TEXT NOT NULL DEFAULT ''`},
		{"author", `TEXT NOT NULL DEFAULT ''`},
		{"host_request", `BLOB NOT NULL DEFAULT ''`},
		{"uploads", `TEXT NOT NULL DEFAULT '[]'`},
		{"refused", `INTEGER NOT NULL DEFAULT 0`},
		{"refusal", `INTEGER NOT NULL DEFAULT 0`},
		{"refusal_announced", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(tx, "message_queue", column.name, column.decl); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name string
		decl string
	}{
		{"source", `TEXT NOT NULL DEFAULT ''`},
		{"transport", `TEXT NOT NULL DEFAULT ''`},
		{"configured_model", `TEXT NOT NULL DEFAULT ''`},
		{"bridge", `TEXT NOT NULL DEFAULT ''`},
		{"forked_from", `TEXT NOT NULL DEFAULT ''`},
		{"last_active_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"parent_session_id", `TEXT NOT NULL DEFAULT ''`},
		{"root_session_id", `TEXT NOT NULL DEFAULT ''`},
		{"subagent_depth", `INTEGER NOT NULL DEFAULT 0`},
		{"agent_nickname", `TEXT NOT NULL DEFAULT ''`},
		{"agent_role", `TEXT NOT NULL DEFAULT ''`},
		{"host_id", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(tx, "sessions", column.name, column.decl); err != nil {
			return err
		}
	}
	// The catalog lists sessions most recent first; reading them in index order
	// spares sorting every row's projection on each snapshot.
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS sessions_recent ON sessions(last_active_ms DESC, updated_at DESC, id)`); err != nil {
		return err
	}
	for _, column := range []struct{ name, decl string }{
		{"relay_hops", `INTEGER NOT NULL DEFAULT 0`},
		{"notification_armed", `INTEGER NOT NULL DEFAULT 0`},
		{"runtime_launcher", `TEXT NOT NULL DEFAULT ''`},
		{"runtime_location", `BLOB NOT NULL DEFAULT '{}'`},
		{"runtime_focusable", `INTEGER NOT NULL DEFAULT 0`},
		{"waiting_kind", `TEXT NOT NULL DEFAULT ''`},
		{"waiting_id", `TEXT NOT NULL DEFAULT ''`},
		{"waiting_since_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"waiting_announced", `INTEGER NOT NULL DEFAULT 0`},
		{"waiting_answerable", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(tx, "session_runtime", column.name, column.decl); err != nil {
			return err
		}
	}
	turnObservedExisted, columnErr := columnExists(tx, "session_runtime", "turn_observed")
	if columnErr != nil {
		return columnErr
	}
	if err := ensureColumn(tx, "session_runtime", "turn_observed", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if !turnObservedExisted {
		if _, err := tx.Exec(`UPDATE session_runtime SET turn_observed=1 WHERE last_status<>'unknown' OR active_turn_id<>'' OR completed_turn_id<>'' OR relay_hops<>0`); err != nil {
			return err
		}
	}
	if err := ensureColumn(tx, "launcher_pending", "alias", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "launcher_pending", "token", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "launcher_pending", "expires_at_ms", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "source_epoch", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "pruned_before", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "seed_status", `TEXT NOT NULL DEFAULT 'unknown'`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "seed_seq", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "seed_identity", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "history_before", `INTEGER NOT NULL DEFAULT -1`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_state", "transcript_checkpoint", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal", "body_ref", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "catalog_state", "projection_clock", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE catalog_state SET projection_clock=(SELECT MAX(projection_generation) FROM catalog_sessions) WHERE projection_clock<(SELECT COALESCE(MAX(projection_generation),0) FROM catalog_sessions)`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal", "position", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if version < 33 {
		if _, err := tx.Exec(`UPDATE session_journal SET position=seq WHERE position=0`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS session_journal_position ON session_journal(session_id, position)`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "delivery_intents", "message", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "delivery_intents", "queue_id", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "delivery_intents", "dismissed_at", `TEXT`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "delivery_intents", "retries", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	for _, column := range []struct{ name, decl string }{
		{"handoff", `TEXT NOT NULL DEFAULT 'turn_finished'`},
		{"event_cursor", `INTEGER NOT NULL DEFAULT 0`},
		{"last_goal_key", `TEXT NOT NULL DEFAULT ''`},
		{"last_error", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(tx, "routes", column.name, column.decl); err != nil {
			return err
		}
	}
	for _, column := range []struct{ name, decl string }{
		{"misses", `INTEGER NOT NULL DEFAULT 0`},
		{"projection_fingerprint", `TEXT NOT NULL DEFAULT ''`},
		{"projection_generation", `INTEGER NOT NULL DEFAULT 0`},
		{"discovery_failures", `INTEGER NOT NULL DEFAULT 0`},
		{"waiting_on", `BLOB NOT NULL DEFAULT ''`},
		{"host_queue_count", `INTEGER NOT NULL DEFAULT 0`},
		{"background_tasks", `BLOB NOT NULL DEFAULT ''`},
		{"usage_limit", `BLOB NOT NULL DEFAULT ''`},
		{"current", `INTEGER NOT NULL DEFAULT 0`},
		{"current_reason", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(tx, "catalog_sessions", column.name, column.decl); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(catalogCurrentTriggers); err != nil {
		return err
	}
	if version < 29 {
		if _, err := tx.Exec(`UPDATE catalog_sessions SET ` + catalogCurrentAssignment("catalog_sessions")); err != nil {
			return err
		}
	}
	if _, err := catalogHostEpochTx(tx, true); err != nil {
		return err
	}
	if err := ensureColumn(tx, "paired_devices", "host_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "device_pairings", "device_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "delivery_intents", "relayed", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if version < 17 {
		if err := dropQueueExpiry(tx); err != nil {
			return err
		}
	}
	if err := ensureColumn(tx, "delivery_intents", "uuid", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if version < 18 {
		if err := backfillIntentUUIDs(tx); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS delivery_intents_uuid ON delivery_intents(uuid)`); err != nil {
		return err
	}
	if version < 13 {
		if _, err := tx.Exec(`UPDATE delivery_intents SET relayed=1 WHERE queue_id IN (SELECT id FROM message_queue WHERE relay_hops>0) OR provider_key LIKE 'relay:%'`); err != nil {
			return err
		}
	}
	if version < 9 {
		if _, err := tx.Exec(`
			UPDATE session_journal
			SET bytes=length(payload)+CASE WHEN body_ref!='' THEN COALESCE((SELECT length(body) FROM session_journal_bodies WHERE session_journal_bodies.session_id=session_journal.session_id AND session_journal_bodies.ref=session_journal.body_ref),0) ELSE 0 END;
			UPDATE session_journal_state
			SET retained_bytes=COALESCE((SELECT SUM(bytes) FROM session_journal WHERE session_journal.session_id=session_journal_state.session_id),0);`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE message_queue SET evidence='delivered' WHERE status='delivered' AND evidence=''`); err != nil {
		return err
	}
	if version < 14 {
		if err := rekeyClaudeConversations(tx); err != nil {
			return err
		}
	}
	if err := recordRuntimeKinds(tx); err != nil {
		return err
	}
	if version < 19 {
		if err := recordObservedSubagents(tx); err != nil {
			return err
		}
	}
	if version == schemaVersion {
		return nil
	}
	if version >= 1 {
		_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion))
		return err
	}
	_, err := tx.Exec(fmt.Sprintf(`
		UPDATE message_queue SET status=CASE WHEN delivered=1 THEN 'delivered' ELSE 'pending' END
		WHERE status='' OR (delivered=1 AND status!='delivered');
		DELETE FROM aliases WHERE rowid NOT IN (SELECT MAX(rowid) FROM aliases GROUP BY session_id);
		CREATE UNIQUE INDEX IF NOT EXISTS message_queue_delivery_key
		ON message_queue(delivery_key) WHERE delivery_key!='';
		CREATE UNIQUE INDEX IF NOT EXISTS aliases_session_id ON aliases(session_id);`))
	if err != nil {
		return err
	}
	_, err = tx.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion))
	return err
}

// catalogCurrentAssignment sets a catalog row's current and current_reason
// from its projection, so readers filter and list on the columns instead of
// parsing every projection.
func catalogCurrentAssignment(row string) string {
	projection := row + ".projection_fingerprint"
	return `current=CASE WHEN json_valid(` + projection + `) THEN COALESCE(json_extract(` + projection + `,'$.current'),0) ELSE 0 END,
		current_reason=CASE WHEN json_valid(` + projection + `) THEN COALESCE(json_extract(` + projection + `,'$.currentReason'),'') ELSE '' END`
}

// catalogCurrentTriggers keep current and current_reason equal to the
// projection in the statement that writes it, whichever writer that is.
var catalogCurrentTriggers = `
DROP TRIGGER IF EXISTS catalog_sessions_current_insert;
CREATE TRIGGER catalog_sessions_current_insert AFTER INSERT ON catalog_sessions
BEGIN
	UPDATE catalog_sessions SET ` + catalogCurrentAssignment("NEW") + ` WHERE session_id=NEW.session_id;
END;
DROP TRIGGER IF EXISTS catalog_sessions_current_update;
CREATE TRIGGER catalog_sessions_current_update AFTER UPDATE OF projection_fingerprint ON catalog_sessions
BEGIN
	UPDATE catalog_sessions SET ` + catalogCurrentAssignment("NEW") + ` WHERE session_id=NEW.session_id;
END;`

// backfillIntentUUIDs gives each delivery intent recorded before intents
// carried a uuid its own, so the unique index can be built over them.
func backfillIntentUUIDs(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE delivery_intents SET uuid=` + sqlRandomUUID + ` WHERE uuid=''`)
	return err
}

// dropQueueExpiry retires the old one-hour queue expiry. Rows it had expired
// become canceled history; dead rows start their review window at their last
// update.
func dropQueueExpiry(tx *sql.Tx) error {
	exists, err := columnExists(tx, "message_queue", "expires_at_ms")
	if err != nil || !exists {
		return err
	}
	const reason = "expired before delivery"
	for _, step := range []struct {
		query string
		args  []any
	}{
		{`UPDATE message_queue SET dead_at_ms=CAST(strftime('%s',updated_at) AS INTEGER)*1000 WHERE status='dead' AND dead_at_ms=0`, nil},
		{`UPDATE message_queue SET status='canceled',evidence='canceled',last_error=? WHERE status='expired'`, []any{reason}},
		{`UPDATE delivery_intents SET status='failed',evidence='failed',failure=? WHERE status='expired'`, []any{reason}},
		{`UPDATE delivery_history SET kind='canceled' WHERE kind='expired'`, nil},
		{`ALTER TABLE message_queue DROP COLUMN expires_at_ms`, nil},
	} {
		if _, err := tx.Exec(step.query, step.args...); err != nil {
			return err
		}
	}
	return nil
}

// setAsideReleasedProjectsTable renames the projects table that v0.2.43 and
// v0.2.44 created (kind, root_path, display_name, registered) so the schema can
// create the current one.
func setAsideReleasedProjectsTable(tx *sql.Tx) error {
	released, err := columnExists(tx, "projects", "root_path")
	if err != nil || !released {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE projects RENAME TO projects_released`)
	return err
}

// upgradeReleasedProjectsTable carries the released table's rows into the
// current one: a registered folder becomes an added project, and an archived
// repository becomes the archive row of its implicit project. Rows that share a
// root collapse into one, keeping the added project and the latest archive time.
func upgradeReleasedProjectsTable(tx *sql.Tx) error {
	var name string
	err := tx.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='projects_released'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO projects(id,workspace_root,title,added,archived_at_ms)
		SELECT 'p-' || MIN(id), root_path, MIN(display_name), 1,
			(SELECT MAX(archived_at_ms) FROM projects_released other WHERE other.root_path=folder.root_path)
		FROM projects_released folder WHERE registered=1 GROUP BY root_path`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO projects(id,workspace_root,title,added,archived_at_ms)
		SELECT 'implicit:' || root_path, root_path, MIN(display_name), 0, MAX(archived_at_ms)
		FROM projects_released WHERE registered=0 AND archived_at_ms>0
			AND root_path NOT IN (SELECT workspace_root FROM projects)
		GROUP BY root_path`); err != nil {
		return err
	}
	_, err = tx.Exec(`DROP TABLE projects_released`)
	return err
}

func columnExists(tx *sql.Tx, table, name string) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var columnName, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return false, err
		}
		if columnName == name {
			found = true
		}
	}
	return found, rows.Close()
}

func ensureColumn(tx *sql.Tx, table, name, declaration string) error {
	found, err := columnExists(tx, table, name)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + name + ` ` + declaration)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '',
	has_local INTEGER NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT '', transport TEXT NOT NULL DEFAULT '',
	runtime_kind TEXT NOT NULL DEFAULT '',
	configured_model TEXT NOT NULL DEFAULT '',
	bridge TEXT NOT NULL DEFAULT '', forked_from TEXT NOT NULL DEFAULT '',
	last_active_ms INTEGER NOT NULL DEFAULT 0,
	registered_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	parent_session_id TEXT NOT NULL DEFAULT '', root_session_id TEXT NOT NULL DEFAULT '',
	subagent_depth INTEGER NOT NULL DEFAULT 0,
	agent_nickname TEXT NOT NULL DEFAULT '', agent_role TEXT NOT NULL DEFAULT '',
	host_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS coordinators (
	project_id TEXT NOT NULL,
	host_id TEXT NOT NULL DEFAULT '',
	project_root TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL,
	surface TEXT NOT NULL,
	cwd TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	permission_mode TEXT NOT NULL DEFAULT '',
	instructions TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	changed_by TEXT NOT NULL DEFAULT '',
	changed_at_ms INTEGER NOT NULL,
	PRIMARY KEY (project_id, host_id),
	UNIQUE (name, host_id)
);
CREATE TABLE IF NOT EXISTS coordinator_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id TEXT NOT NULL,
	action TEXT NOT NULL CHECK (action IN ('set','replaced','cleared')),
	name TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	previous_name TEXT NOT NULL DEFAULT '',
	previous_session_id TEXT NOT NULL DEFAULT '',
	changed_by TEXT NOT NULL DEFAULT '',
	at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS coordinator_replaced (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	project_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_rekeys (
	old_id TEXT PRIMARY KEY, new_id TEXT NOT NULL,
	rekeyed_at TEXT NOT NULL DEFAULT (datetime('now'))
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
	once_only INTEGER NOT NULL DEFAULT 0,
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	handoff TEXT NOT NULL DEFAULT 'turn_finished',
	event_cursor INTEGER NOT NULL DEFAULT 0,
	last_goal_key TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT ''
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
	source_session_id TEXT NOT NULL DEFAULT '',
	turn_options TEXT NOT NULL DEFAULT '{}',
	dead_at_ms INTEGER NOT NULL DEFAULT 0,
	deferrals INTEGER NOT NULL DEFAULT 0,
	evidence TEXT NOT NULL DEFAULT '',
	operation TEXT NOT NULL DEFAULT 'message',
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	busy_delivery TEXT NOT NULL DEFAULT '',
	author TEXT NOT NULL DEFAULT '',
	host_request BLOB NOT NULL DEFAULT '',
	uploads TEXT NOT NULL DEFAULT '[]',
	refused INTEGER NOT NULL DEFAULT 0,
	refusal INTEGER NOT NULL DEFAULT 0,
	refusal_announced INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_runtime (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	last_status TEXT NOT NULL DEFAULT 'unknown',
	active_turn_id TEXT NOT NULL DEFAULT '',
	completed_turn_id TEXT NOT NULL DEFAULT '',
	relay_hops INTEGER NOT NULL DEFAULT 0,
	notification_armed INTEGER NOT NULL DEFAULT 0,
	turn_observed INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	runtime_launcher TEXT NOT NULL DEFAULT '',
	runtime_location BLOB NOT NULL DEFAULT '{}',
	runtime_focusable INTEGER NOT NULL DEFAULT 0,
	waiting_kind TEXT NOT NULL DEFAULT '',
	waiting_id TEXT NOT NULL DEFAULT '',
	waiting_since_ms INTEGER NOT NULL DEFAULT 0,
	waiting_announced INTEGER NOT NULL DEFAULT 0,
	waiting_answerable INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_origins (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	via TEXT NOT NULL,
	creator_session_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS session_context (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	fingerprint TEXT NOT NULL,
	start_key TEXT NOT NULL DEFAULT '',
	delivered_at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS launcher_pending (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	launcher TEXT NOT NULL,
	agent TEXT NOT NULL,
	cwd TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	alias TEXT NOT NULL DEFAULT '',
	token TEXT NOT NULL DEFAULT '',
	expires_at_ms INTEGER NOT NULL DEFAULT 0,
	location BLOB NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS alias_reservations (
	name TEXT PRIMARY KEY,
	token TEXT NOT NULL,
	expires_at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS relay_sources (
    source_key TEXT PRIMARY KEY,
    cursor TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS relay_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_key TEXT NOT NULL,
    completion_id TEXT NOT NULL,
    input_id TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    relay_hops INTEGER NOT NULL DEFAULT 0,
    completed_at_ms INTEGER NOT NULL DEFAULT 0,
    captured_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_key,completion_id)
);
CREATE INDEX IF NOT EXISTS relay_events_source ON relay_events(source_key,id);
CREATE TABLE IF NOT EXISTS relay_provenance (
    queue_id INTEGER PRIMARY KEY REFERENCES message_queue(id) ON DELETE CASCADE,
    input_id TEXT NOT NULL DEFAULT '',
    completion_id TEXT NOT NULL DEFAULT ''
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
	consumed_at TEXT NOT NULL DEFAULT '',
	device_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS device_pairings_expires ON device_pairings(expires_at);
CREATE TABLE IF NOT EXISTS paired_devices (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	scopes TEXT NOT NULL,
	host_id TEXT NOT NULL DEFAULT '',
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
CREATE TABLE IF NOT EXISTS session_journal (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	position INTEGER NOT NULL DEFAULT 0,
	kind TEXT NOT NULL,
	provider_key TEXT NOT NULL DEFAULT '',
	payload BLOB NOT NULL,
	observed_at TEXT NOT NULL,
	bytes INTEGER NOT NULL,
	body_ref TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, seq)
);
CREATE UNIQUE INDEX IF NOT EXISTS session_journal_provider_key
	ON session_journal(session_id, provider_key) WHERE provider_key!='';
CREATE TABLE IF NOT EXISTS session_journal_state (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	next_seq INTEGER NOT NULL DEFAULT 0,
	retained_bytes INTEGER NOT NULL DEFAULT 0,
	source_epoch TEXT NOT NULL DEFAULT '',
	pruned_before INTEGER NOT NULL DEFAULT 0,
	seed_status TEXT NOT NULL DEFAULT 'unknown',
	seed_seq INTEGER NOT NULL DEFAULT 0,
	seed_identity TEXT NOT NULL DEFAULT '',
	history_before INTEGER NOT NULL DEFAULT -1,
	derived_version INTEGER NOT NULL DEFAULT 0,
	transcript_checkpoint TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS session_journal_bodies (
	ref TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	body BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS session_journal_bodies_session ON session_journal_bodies(session_id);
CREATE TABLE IF NOT EXISTS action_receipts (
	idempotency_key TEXT PRIMARY KEY,
	request_hash TEXT NOT NULL,
	status TEXT NOT NULL,
	receipt BLOB NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS delivery_intents (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sender_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	target_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	provider_key TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	queue_id INTEGER NOT NULL DEFAULT 0,
	relayed INTEGER NOT NULL DEFAULT 0,
	uuid TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	evidence TEXT NOT NULL,
	failure TEXT NOT NULL DEFAULT '',
	dismissed_at TEXT,
	notification_queue_id INTEGER REFERENCES message_queue(id) ON DELETE SET NULL,
	retries INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS delivery_intents_provider_key
	ON delivery_intents(target_session_id, provider_key) WHERE provider_key!='';
CREATE INDEX IF NOT EXISTS delivery_intents_sender_status
	ON delivery_intents(sender_session_id, status, id DESC);
CREATE TABLE IF NOT EXISTS catalog_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	dedupe_key TEXT NOT NULL UNIQUE,
	type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS catalog_events_created ON catalog_events(created_at DESC, seq DESC);
DROP TRIGGER IF EXISTS message_queue_catalog_insert;
CREATE TRIGGER message_queue_catalog_insert AFTER INSERT ON message_queue
WHEN NEW.status IN ('pending','inflight') AND NOT EXISTS(SELECT 1 FROM sessions WHERE id=NEW.session_id AND host_id<>'')
BEGIN
	INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at)
	VALUES('session.queue:'||NEW.session_id||':'||lower(hex(randomblob(12))),'session.queue',NEW.session_id,
		json_object('sessionId',NEW.session_id,'queueCount',(SELECT COUNT(*) FROM message_queue WHERE session_id=NEW.session_id AND status IN ('pending','inflight'))),
		strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
DROP TRIGGER IF EXISTS message_queue_catalog_update;
CREATE TRIGGER message_queue_catalog_update AFTER UPDATE OF status,session_id ON message_queue
WHEN ((OLD.status IN ('pending','inflight')) != (NEW.status IN ('pending','inflight')) OR (OLD.session_id != NEW.session_id AND NEW.status IN ('pending','inflight'))) AND NOT EXISTS(SELECT 1 FROM sessions WHERE id=NEW.session_id AND host_id<>'')
BEGIN
	INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at)
	VALUES('session.queue:'||NEW.session_id||':'||lower(hex(randomblob(12))),'session.queue',NEW.session_id,
		json_object('sessionId',NEW.session_id,'queueCount',(SELECT COUNT(*) FROM message_queue WHERE session_id=NEW.session_id AND status IN ('pending','inflight'))),
		strftime('%Y-%m-%dT%H:%M:%fZ','now'));
	INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at)
	SELECT 'session.queue:'||OLD.session_id||':'||lower(hex(randomblob(12))),'session.queue',OLD.session_id,
		json_object('sessionId',OLD.session_id,'queueCount',(SELECT COUNT(*) FROM message_queue WHERE session_id=OLD.session_id AND status IN ('pending','inflight'))),
		strftime('%Y-%m-%dT%H:%M:%fZ','now')
	WHERE OLD.session_id != NEW.session_id AND OLD.status IN ('pending','inflight');
END;
DROP TRIGGER IF EXISTS message_queue_catalog_delete;
CREATE TRIGGER message_queue_catalog_delete AFTER DELETE ON message_queue
WHEN OLD.status IN ('pending','inflight') AND NOT EXISTS(SELECT 1 FROM sessions WHERE id=OLD.session_id AND host_id<>'')
BEGIN
	INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at)
	VALUES('session.queue:'||OLD.session_id||':'||lower(hex(randomblob(12))),'session.queue',OLD.session_id,
		json_object('sessionId',OLD.session_id,'queueCount',(SELECT COUNT(*) FROM message_queue WHERE session_id=OLD.session_id AND status IN ('pending','inflight'))),
		strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
CREATE TABLE IF NOT EXISTS host_identity (
	id INTEGER PRIMARY KEY CHECK (id=1),
	host_id TEXT NOT NULL,
	name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS hosts (
	host_id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	endpoint TEXT NOT NULL,
	address TEXT NOT NULL,
	token TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'connecting',
	last_error TEXT NOT NULL DEFAULT '',
	last_contact_at TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	catalog_epoch TEXT NOT NULL DEFAULT '',
	catalog_seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS catalog_sessions (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	host_project BLOB NOT NULL,
	checkout BLOB NOT NULL,
	unavailable_reason TEXT NOT NULL DEFAULT '',
	observed_at TEXT NOT NULL,
	misses INTEGER NOT NULL DEFAULT 0,
	projection_fingerprint TEXT NOT NULL DEFAULT '',
	projection_generation INTEGER NOT NULL DEFAULT 0,
	discovery_failures INTEGER NOT NULL DEFAULT 0,
	waiting_on BLOB NOT NULL DEFAULT '',
	host_queue_count INTEGER NOT NULL DEFAULT 0,
	background_tasks BLOB NOT NULL DEFAULT '',
	usage_limit BLOB NOT NULL DEFAULT '',
	current INTEGER NOT NULL DEFAULT 0,
	current_reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS catalog_surfaces (
	surface TEXT PRIMARY KEY,
	health TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	observed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
	id TEXT PRIMARY KEY,
	workspace_root TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	added INTEGER NOT NULL DEFAULT 0,
	project_key TEXT NOT NULL DEFAULT '',
	repository BLOB NOT NULL DEFAULT '',
	created_at_ms INTEGER NOT NULL DEFAULT 0,
	archived_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_archive (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	archived_at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_state (
	id INTEGER PRIMARY KEY CHECK (id=1),
	host_epoch TEXT NOT NULL,
	projection_clock INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS signin_profiles (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL UNIQUE,
	browser TEXT NOT NULL,
	profile TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(browser, profile)
);
CREATE TABLE IF NOT EXISTS signin_bindings (
	consumer TEXT PRIMARY KEY,
	profile_id TEXT NOT NULL REFERENCES signin_profiles(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS signin_off (
	consumer TEXT PRIMARY KEY
);
CREATE TABLE IF NOT EXISTS session_moves (
	id TEXT PRIMARY KEY,
	conversation TEXT NOT NULL,
	peer_host TEXT NOT NULL,
	role TEXT NOT NULL CHECK (role IN ('out','in')),
	state TEXT NOT NULL,
	manifest BLOB NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS session_moves_conversation ON session_moves(conversation, role, state);
`

func (r *Registry) RegisterSession(s surface.Session) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := registerSessionTx(tx, s); err != nil {
		return err
	}
	return tx.Commit()
}

func registerSessionTx(tx *sql.Tx, s surface.Session) error {
	if s.RuntimeKind == "" {
		return fmt.Errorf("register session %s: no runtime kind", s.ID)
	}
	var owner string
	if err := tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, s.ID).Scan(&owner); err != nil && err != sql.ErrNoRows {
		return err
	}
	if owner != "" {
		if err := releaseUnservedHostRowTx(tx, s.ID, owner); err != nil {
			return err
		}
	}
	if s.HolderPID > 0 {
		// A session addressed to one of the processes that hold its
		// conversation is a write target, not an observation of which
		// processes hold it; discovery records that. The row only has to
		// exist for the write's history.
		_, err := tx.Exec(`INSERT INTO sessions (id,surface,name,cwd,status,transcript,has_local,source,runtime_kind,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,datetime('now')) ON CONFLICT(id) DO NOTHING`,
			s.ID, string(s.Surface), s.Name, s.Cwd, string(s.Status), s.Transcript, b2i(s.HasLocal), s.Source, string(s.RuntimeKind))
		return err
	}
	lastActiveMS := int64(0)
	if !s.LastActive.IsZero() {
		lastActiveMS = s.LastActive.UnixMilli()
	}
	parentID, rootID, depth, nickname, role := subagentValues(s)
	_, err := tx.Exec(
		`INSERT INTO sessions (id,surface,name,cwd,pid,status,transcript,has_local,source,transport,runtime_kind,configured_model,bridge,forked_from,last_active_ms,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,updated_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET surface=excluded.surface,name=excluded.name,cwd=excluded.cwd,
		   pid=excluded.pid,status=excluded.status,transcript=excluded.transcript,
		   has_local=excluded.has_local,
		   source=CASE
		     WHEN sessions.surface='codex' AND sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop') THEN sessions.source
		     WHEN sessions.surface='codex' AND sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed' THEN sessions.source
		     WHEN excluded.source<>'' THEN excluded.source
		     ELSE sessions.source
		   END,
		   transport=CASE
		     WHEN sessions.surface='codex' AND sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop') THEN sessions.transport
		     WHEN sessions.surface='codex' AND sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed' THEN sessions.transport
		     WHEN excluded.transport<>'' THEN excluded.transport
		     ELSE sessions.transport
			   END,
			   runtime_kind=CASE
		     WHEN sessions.surface='codex' AND sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop') THEN sessions.runtime_kind
		     WHEN sessions.surface='codex' AND sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed' THEN sessions.runtime_kind
		     ELSE excluded.runtime_kind
			   END,
			   configured_model=CASE WHEN excluded.configured_model<>'' THEN excluded.configured_model ELSE sessions.configured_model END,
			   bridge=excluded.bridge,
			   forked_from=CASE WHEN sessions.forked_from<>'' THEN sessions.forked_from ELSE excluded.forked_from END,
			   last_active_ms=excluded.last_active_ms,
		   parent_session_id=CASE WHEN excluded.parent_session_id<>'' THEN excluded.parent_session_id ELSE sessions.parent_session_id END,
		   root_session_id=CASE WHEN excluded.root_session_id<>'' THEN excluded.root_session_id ELSE sessions.root_session_id END,
		   subagent_depth=CASE WHEN excluded.parent_session_id<>'' THEN excluded.subagent_depth ELSE sessions.subagent_depth END,
		   agent_nickname=CASE WHEN excluded.agent_nickname<>'' THEN excluded.agent_nickname ELSE sessions.agent_nickname END,
		   agent_role=CASE WHEN excluded.agent_role<>'' THEN excluded.agent_role ELSE sessions.agent_role END,
		   updated_at=datetime('now')`,
		s.ID, string(s.Surface), s.Name, s.Cwd, s.PID, string(s.Status), s.Transcript, b2i(s.HasLocal), s.Source, s.Transport, string(s.RuntimeKind), s.ConfiguredModel, s.Bridge, s.ForkedFrom, lastActiveMS, parentID, rootID, depth, nickname, role)
	if err != nil {
		return err
	}
	if s.Runtime != nil {
		location, err := json.Marshal(s.Runtime.Location)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,runtime_launcher,runtime_location,runtime_focusable,updated_at) VALUES(?,?,?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET runtime_launcher=excluded.runtime_launcher,runtime_location=excluded.runtime_location,runtime_focusable=excluded.runtime_focusable,updated_at=datetime('now')`, s.ID, string(s.Runtime.Launcher), location, b2i(s.Runtime.Focusable)); err != nil {
			return err
		}
	}
	if s.Surface == surface.KindClaude {
		pids := make([]int, 0, 1+len(s.Holders))
		if s.PID > 0 {
			pids = append(pids, s.PID)
		}
		for _, holder := range s.Holders {
			pids = append(pids, holder.PID)
		}
		for _, pid := range pids {
			if _, err := tx.Exec(`UPDATE sessions SET pid=0,bridge='',updated_at=datetime('now') WHERE surface=? AND pid=? AND id<>?`, string(surface.KindClaude), pid, s.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Registry) LookupAlias(name string) (string, error) {
	var sid string
	err := r.read.QueryRow(`SELECT session_id FROM aliases WHERE name = ?`, name).Scan(&sid)
	return sid, err
}

func (r *Registry) SetAlias(name, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setAliasTx(tx, name, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

type AliasTakenError struct {
	Name  string
	Owner string
}

func (e AliasTakenError) Error() string {
	return fmt.Sprintf("alias %q is already assigned to session %q", e.Name, e.Owner)
}

// setAliasTx names a session. A coordinator's name moves only through the
// coordinator, so it is neither given to another session nor taken from the
// session it names, and a name a session being started holds waits for it.
func setAliasTx(tx *sql.Tx, name, sessionID string) error {
	if strings.EqualFold(name, LeadAlias) {
		return ErrReservedAlias
	}
	var current string
	err := tx.QueryRow(`SELECT name FROM aliases WHERE session_id=?`, sessionID).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if current == name {
		return nil
	}
	for _, held := range []string{name, current} {
		if held == "" {
			continue
		}
		if coordinated, err := coordinatorNamedTx(tx, held); err != nil {
			return err
		} else if coordinated {
			return CoordinatorAliasError{Name: held}
		}
	}
	if err := aliasReservedTx(tx, name, ""); err != nil {
		return err
	}
	return bindAliasTx(tx, name, sessionID)
}

func bindAliasTx(tx *sql.Tx, name, sessionID string) error {
	if strings.EqualFold(name, LeadAlias) {
		return ErrReservedAlias
	}
	var owner string
	err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && owner != sessionID {
		return AliasTakenError{Name: name, Owner: owner}
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?)`, name, sessionID)
	return err
}

func (r *Registry) ReserveGeneratedAlias(sessionID, base string) (string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existing string
	if err := tx.QueryRow(`SELECT name FROM aliases WHERE session_id=?`, sessionID).Scan(&existing); err == nil {
		return existing, tx.Commit()
	} else if err != sql.ErrNoRows {
		return "", err
	}
	base = generatedAliasCharacters.ReplaceAllString(strings.ToLower(base), "-")
	base = strings.Trim(base, "- ")
	if base == "" {
		base = "agent"
	}
	for n := 0; n < 1000; n++ {
		candidate := base
		if n > 0 {
			candidate = fmt.Sprintf("%s-%d", base, n+1)
		}
		if candidate == LeadAlias {
			continue
		}
		if coordinated, err := coordinatorNamedTx(tx, candidate); err != nil {
			return "", err
		} else if coordinated {
			continue
		}
		var reserved AliasReservedError
		if err := aliasReservedTx(tx, candidate, ""); errors.As(err, &reserved) {
			continue
		} else if err != nil {
			return "", err
		}
		if _, err := tx.Exec(`INSERT INTO aliases(name,session_id) VALUES(?,?)`, candidate, sessionID); err == nil {
			return candidate, tx.Commit()
		} else if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", fmt.Errorf("reserve generated alias %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("reserve generated alias: exhausted candidates for %q", base)
}

// ResolveTarget resolves an alias, an exact ID, a unique ID prefix, or a
// name/cwd fragment; "@<name>" resolves only an alias or exact ID, so a
// name never reaches another session by substring. Name and cwd fragments
// match only family roots, so a
// family resolves to its root. "@<parent>/<child>[/<child>...]" walks
// subagents: <parent> is any other target form and each <child> is a direct
// subagent's nickname (case-insensitive), ID, or unique ID prefix.
func (r *Registry) ResolveTarget(target string) (string, error) {
	if path, ok := strings.CutPrefix(target, "@"); ok {
		if strings.Contains(path, "/") {
			return r.resolveSubagentPath(path)
		}
		return r.ExactTarget("", path)
	}
	return r.resolveTarget(target, "")
}

// ResolveSurfaceTarget resolves a target like ResolveTarget, but matches and
// judges ambiguity only among sessions of the given surface.
func (r *Registry) ResolveSurfaceTarget(kind surface.SurfaceKind, target string) (string, error) {
	if name, ok := strings.CutPrefix(target, "@"); ok {
		return r.ExactTarget(kind, name)
	}
	return r.resolveTarget(target, string(kind))
}

func (r *Registry) resolveTarget(target, kind string) (string, error) {
	if sid, err := r.ExactTarget(surface.SurfaceKind(kind), target); err == nil {
		return sid, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if ids, err := r.idPrefixMatches(target, kind, 2); err != nil {
		return "", err
	} else if len(ids) == 1 {
		return ids[0], nil
	} else if len(ids) > 1 {
		return "", fmt.Errorf("ambiguous session id prefix %q matches %s", target, strings.Join(ids, ", "))
	}
	ids, err := r.nameOrCwdMatches(target, kind, 2)
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

// NamesSession reports whether selector identifies session on its own rather
// than as a name or cwd fragment: an alias, an ID or ID prefix, a Claude
// conversation ID or PID, or @<parent>/<nickname> under such a parent.
func (r *Registry) NamesSession(session surface.Session, selector string) bool {
	if path, ok := strings.CutPrefix(selector, "@"); ok {
		if parent, _, isPath := strings.Cut(path, "/"); isPath {
			_, err := r.ExactTarget("", parent)
			return err == nil
		}
	}
	if surface.IdentifiedBy(session, strings.TrimPrefix(selector, "@")) {
		return true
	}
	sid, err := r.ExactTarget(session.Surface, strings.TrimPrefix(selector, "@"))
	return err == nil && sid == session.ID
}

// FragmentMatches lists the local sessions target matches by ID prefix or
// name/cwd fragment, newest first; an empty kind means every surface.
func (r *Registry) FragmentMatches(kind surface.SurfaceKind, target string, limit int) ([]string, error) {
	if target == "" {
		return nil, nil
	}
	prefixed, err := r.idPrefixMatches(target, string(kind), limit)
	if err != nil {
		return nil, err
	}
	named, err := r.nameOrCwdMatches(target, string(kind), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	for _, id := range append(prefixed, named...) {
		if !seen[id] && len(ids) < limit {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (r *Registry) idPrefixMatches(target, kind string, limit int) ([]string, error) {
	return r.matchingIDs(`SELECT id FROM sessions WHERE host_id='' AND id LIKE ? ESCAPE '\' AND (? = '' OR surface = ?) ORDER BY updated_at DESC, id LIMIT ?`, escapeLike(target)+"%", kind, kind, limit)
}

// nameOrCwdMatches matches only family roots, so a fragment never picks a
// subagent.
func (r *Registry) nameOrCwdMatches(target, kind string, limit int) ([]string, error) {
	contains := "%" + escapeLike(target) + "%"
	return r.matchingIDs(`SELECT id FROM sessions WHERE host_id='' AND parent_session_id='' AND (name LIKE ? ESCAPE '\' OR cwd LIKE ? ESCAPE '\') AND (? = '' OR surface = ?) ORDER BY updated_at DESC, id LIMIT ?`, contains, contains, kind, kind, limit)
}

func (r *Registry) localSessionSurface(id string) (string, bool) {
	var kind string
	err := r.read.QueryRow(`SELECT surface FROM sessions WHERE id = ? AND host_id=''`, id).Scan(&kind)
	return kind, err == nil
}

// ExactTarget resolves only targets that name one session of surface kind
// exactly, any surface when kind is empty: an alias, a session ID, or the
// conversation ID a Claude transcript is named after.
func (r *Registry) ExactTarget(kind surface.SurfaceKind, target string) (string, error) {
	if sid, err := r.LookupAlias(strings.TrimPrefix(target, "@")); err == nil {
		if aliased, local := r.localSessionSurface(sid); local && (kind == "" || aliased == string(kind)) {
			return sid, nil
		}
	}
	var exact string
	err := r.read.QueryRow(`SELECT id FROM sessions WHERE id = ? AND host_id='' AND (? = '' OR surface = ?)`, target, string(kind), string(kind)).Scan(&exact)
	if err == nil || !errors.Is(err, sql.ErrNoRows) || target == "" || (kind != "" && kind != surface.KindClaude) {
		return exact, err
	}
	err = r.read.QueryRow(`SELECT id FROM sessions WHERE surface=? AND host_id='' AND transcript LIKE ? ESCAPE '\' ORDER BY updated_at DESC, rowid DESC LIMIT 1`, string(surface.KindClaude), "%/"+escapeLike(target)+".jsonl").Scan(&exact)
	return exact, err
}

func (r *Registry) resolveSubagentPath(path string) (string, error) {
	segments := strings.Split(path, "/")
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			return "", fmt.Errorf("invalid subagent target %q: use @<parent>/<nickname>", "@"+path)
		}
	}
	current, err := r.ResolveTarget(segments[0])
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("subagent target %q: no session matched %q", "@"+path, segments[0])
	}
	if err != nil {
		return "", fmt.Errorf("subagent target %q: %w", "@"+path, err)
	}
	for _, segment := range segments[1:] {
		rows, err := r.read.Query(`SELECT id,agent_nickname FROM sessions WHERE parent_session_id=? ORDER BY id`, current)
		if err != nil {
			return "", err
		}
		var nicknameMatches, idMatches, prefixMatches []string
		for rows.Next() {
			var id, nickname string
			if err := rows.Scan(&id, &nickname); err != nil {
				rows.Close()
				return "", err
			}
			switch {
			case id == segment:
				idMatches = append(idMatches, id)
			case nickname != "" && strings.EqualFold(nickname, segment):
				nicknameMatches = append(nicknameMatches, id)
			case strings.HasPrefix(id, segment):
				prefixMatches = append(prefixMatches, id)
			}
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
		var matches []string
		switch {
		case len(idMatches) > 0:
			matches = idMatches
		case len(nicknameMatches) > 0:
			matches = nicknameMatches
		default:
			matches = prefixMatches
		}
		if len(matches) == 0 {
			return "", fmt.Errorf("subagent target %q: %s has no subagent %q", "@"+path, current, segment)
		}
		if len(matches) > 1 {
			return "", fmt.Errorf("ambiguous subagent target %q: %q matches %s; use a subagent ID", "@"+path, segment, strings.Join(matches, ", "))
		}
		current = matches[0]
	}
	return current, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

// NamedSession is a session of this host and the alias it answers to, if any.
type NamedSession struct {
	ID    string
	Alias string
}

// SessionsNamed lists this host's top-level sessions of a surface whose name
// is exactly name, most recently updated first.
func (r *Registry) SessionsNamed(kind surface.SurfaceKind, name string) ([]NamedSession, error) {
	rows, err := r.read.Query(`SELECT s.id, COALESCE((SELECT a.name FROM aliases a WHERE a.session_id = s.id ORDER BY a.created_at LIMIT 1),'') FROM sessions s WHERE s.host_id='' AND s.parent_session_id='' AND s.surface = ? AND s.name = ? ORDER BY s.updated_at DESC, s.id`, string(kind), name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NamedSession
	for rows.Next() {
		var session NamedSession
		if err := rows.Scan(&session.ID, &session.Alias); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (r *Registry) matchingIDs(query string, args ...any) ([]string, error) {
	rows, err := r.read.Query(query, args...)
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
	return r.AddRouteWithOptions(from, to, pattern, false)
}

func (r *Registry) AddRouteWithOptions(from, to, pattern string, once bool) (int64, error) {
	return r.AddRelayRoute(from, to, pattern, once, HandoffTurnFinished)
}

func (r *Registry) AddRelayRoute(from, to, pattern string, once bool, handoff string) (int64, error) {
	if handoff == "" {
		handoff = HandoffTurnFinished
	}
	if handoff != HandoffTurnFinished && handoff != HandoffGoalCompleted {
		return 0, fmt.Errorf("handoff must be turn_finished or goal_completed")
	}
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
			SELECT to_session FROM routes WHERE from_session=? AND active=1
			UNION
			SELECT routes.to_session FROM routes JOIN reachable ON routes.from_session=reachable.id WHERE routes.active=1
		)
		SELECT COUNT(*) FROM reachable WHERE id=?`, to, from).Scan(&cycle)
	if err != nil {
		return 0, err
	}
	if cycle > 0 {
		return 0, fmt.Errorf("relay route would create a cycle")
	}
	res, err := tx.Exec(`INSERT INTO routes (from_session,to_session,pattern,once_only,handoff,event_cursor) VALUES (?,?,?,?,?,COALESCE((SELECT MAX(id) FROM relay_events),0))`, from, to, pattern, b2i(once), handoff)
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
	return r.queueMessageWithOptions(sessionID, message, deliveryKey, options, 0, QueueOperationMessage)
}

func (r *Registry) QueueDeliveryWithIntent(sessionID, message, deliveryKey string, options surface.SendOptions) (int64, int64, error) {
	return r.enqueueMessage(sessionID, message, deliveryKey, options, 0, QueueOperationMessage, true)
}

type QueueOperation string

const (
	QueueOperationMessage QueueOperation = "message"
	QueueOperationCompact QueueOperation = "compact"
	QueueOperationGoal    QueueOperation = "goal"
)

// QueueGoal holds a goal command until the session is idle. Like compact it
// is a control operation that never drains through a message transport.
func (r *Registry) QueueGoal(sessionID, command string) (int64, error) {
	res, err := r.db.Exec(`INSERT INTO message_queue (session_id,message,operation,status,updated_at) VALUES (?,?,?,'pending',datetime('now'))`, sessionID, command, QueueOperationGoal)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: command})
	return id, nil
}

// QueueCompact preserves compact as a control operation. It must never be
// drained through a message transport, where "/compact" would become prompt text.
func (r *Registry) QueueCompact(sessionID string) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRow(`SELECT id FROM message_queue WHERE session_id=? AND operation=? AND status IN ('pending','inflight') ORDER BY id LIMIT 1`, sessionID, QueueOperationCompact).Scan(&existing)
	if err == nil {
		return existing, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,status,updated_at) VALUES (?,?,?,'pending',datetime('now'))`, sessionID, "/compact", QueueOperationCompact)
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
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: "compact"})
	return id, nil
}

func (r *Registry) QueueRelayMessage(sessionID, message, deliveryKey string, relayHops int) (int64, error) {
	return r.QueueRelayMessageWithOptions(sessionID, message, deliveryKey, relayHops, surface.SendOptions{})
}

func (r *Registry) QueueRelayMessageWithOptions(sessionID, message, deliveryKey string, relayHops int, options surface.SendOptions) (int64, error) {
	id, _, err := r.enqueueMessage(sessionID, message, deliveryKey, options, relayHops, QueueOperationMessage, true)
	return id, err
}

func (r *Registry) queueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation) (int64, error) {
	id, _, err := r.enqueueMessage(sessionID, message, deliveryKey, options, relayHops, operation, false)
	return id, err
}

func (r *Registry) enqueueMessage(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, recordIntent bool) (int64, int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	id, deliveryID, err := enqueueMessageTx(tx, r.now(), sessionID, message, deliveryKey, options, relayHops, operation, recordIntent)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: message, Result: options.Model})
	return id, deliveryID, nil
}

func enqueueMessageTx(tx *sql.Tx, now time.Time, sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, recordIntent bool) (int64, int64, error) {
	if options.BusyDelivery == "steer" && options.HoldsForQueue() {
		options.BusyDelivery = "queue"
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,delivery_key,model,source_session_id,turn_options,relay_hops,busy_delivery,author,uploads,status,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,'pending',datetime('now'))`, sessionID, message, operation, deliveryKey, options.Model, options.SourceSessionID, options.TurnOptions, relayHops, options.BusyDelivery, options.Author, options.Uploads)
	if err != nil {
		if deliveryKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			var id int64
			if err := tx.QueryRow(`SELECT id FROM message_queue WHERE delivery_key=? AND delivery_key!=''`, deliveryKey).Scan(&id); err != nil {
				return 0, 0, err
			}
			deliveryID, err := queuedDeliveryIntentID(tx, id)
			return id, deliveryID, err
		}
		return 0, 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	var deliveryID int64
	if recordIntent && options.SourceSessionID != "" {
		if deliveryID, err = insertQueuedDeliveryIntent(tx, options.SourceSessionID, sessionID, message, id, relayHops > 0); err != nil {
			return 0, 0, err
		}
	}
	return id, deliveryID, nil
}

// readExists answers a periodic "is there work?" question from the read pool,
// so an idle pass takes no write lock; the writer transaction checks again.
func (r *Registry) readExists(query string, args ...any) (bool, error) {
	var exists bool
	err := r.read.QueryRow(query, args...).Scan(&exists)
	return exists, err
}

func (r *Registry) QueueCount(sessionID string) int {
	var n int
	r.read.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, sessionID).Scan(&n)
	return n
}

func (r *Registry) PendingSteer(sessionID string) (bool, error) {
	mode, err := r.busyMode()
	if err != nil {
		return false, err
	}
	var found int
	err = r.read.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight') AND `+queueSteerRow("message_queue", mode), sessionID).Scan(&found)
	return found > 0, err
}

func (r *Registry) QueueCounts() (map[string]int, error) {
	rows, err := r.read.Query(`SELECT session_id,COUNT(*) FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id`)
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
	surface.TurnOptions
	ID              int64
	SessionID       string
	Message         string
	Model           string
	SourceSessionID string
	Attempts        int
	RelayHops       int
	Operation       QueueOperation
	BusyDelivery    string
	Author          surface.Author
	Uploads         surface.Uploads
}

const (
	maxHistoryText = 16 * 1024
	maxHistoryRows = 2000
)

// HistoryEntry is the durable, operator-facing record of work moving through
// the daemon. It intentionally stores bounded message/result text so a single
// verbose agent cannot grow the registry without limit.
type HistoryEntry struct {
	ID              int64                    `json:"id"`
	CreatedAt       string                   `json:"createdAt"`
	Kind            string                   `json:"kind"`
	SessionID       string                   `json:"sessionId,omitempty"`
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	RouteID         int64                    `json:"routeId,omitempty"`
	QueueID         int64                    `json:"queueId,omitempty"`
	CompletionID    string                   `json:"completionId,omitempty"`
	Message         string                   `json:"message,omitempty"`
	Result          string                   `json:"result,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Evidence        surface.DeliveryEvidence `json:"evidence,omitempty"`
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
	rows, err := r.read.Query(query, args...)
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
		entry.Evidence = historyEvidence(entry)
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
	rows, err := r.read.Query(query, args...)
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
		entry.Evidence = historyEvidence(entry)
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

func historyEvidence(entry HistoryEntry) surface.DeliveryEvidence {
	switch entry.Kind {
	case "submitted":
		return surface.EvidenceSubmitted
	case "queued":
		return surface.EvidenceQueued
	case "peer_sent", "peer_received", "transport-accepted":
		return surface.EvidenceTransportAccepted
	case "peer_receipt":
		status := strings.ToLower(strings.TrimSpace(strings.SplitN(entry.Result, ":", 2)[0]))
		switch status {
		case "held":
			return surface.EvidenceHeld
		case "delivered", "received", "accepted":
			return surface.EvidenceDelivered
		case "denied", "failed", "rejected":
			return surface.EvidenceFailed
		default:
			return surface.EvidenceUnknown
		}
	case "sent", "delivered", "control-accepted":
		return surface.EvidenceDelivered
	case "reply", "completed":
		return surface.EvidenceReplyObserved
	case "unknown", "ack-error":
		return surface.EvidenceUnknown
	case "failed", "peer_rejected", "control-failed":
		return surface.EvidenceFailed
	case "canceled":
		return surface.EvidenceCanceled
	default:
		return ""
	}
}

func (r *Registry) ListHistoryKinds() ([]string, error) {
	rows, err := r.read.Query(`SELECT DISTINCT kind FROM delivery_history ORDER BY kind`)
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
	surface.TurnOptions
	ID              int64                    `json:"id"`
	SessionID       string                   `json:"sessionId"`
	Message         string                   `json:"message"`
	Model           string                   `json:"model,omitempty"`
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	Status          string                   `json:"status"`
	Attempts        int                      `json:"attempts"`
	LastError       string                   `json:"lastError,omitempty"`
	QueuedAt        string                   `json:"queuedAt"`
	DeadAt          int64                    `json:"-"`
	HeldBy          QueueHold                `json:"heldBy,omitempty"`
	Historical      bool                     `json:"historical"`
	Evidence        surface.DeliveryEvidence `json:"evidence"`
	Operation       QueueOperation           `json:"operation"`
	BusyDelivery    string                   `json:"busyDelivery,omitempty"`
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

// QueueRetryBackoff is how long a queued message waits before it is tried
// again after a failed or deferred attempt. Each later retry doubles it, up to
// 64 times this.
const QueueRetryBackoff = 5 * time.Second

const uncertainDeliveryError = "delivery outcome is unknown after daemon interruption; retry explicitly if the target did not receive it"

// QueueHold says why a pending message has not been delivered yet. Empty
// means nothing holds it and the next scan delivers it.
type QueueHold string

const (
	QueueHeldWaiting  QueueHold = "waiting"
	QueueHeldBusy     QueueHold = "busy"
	QueueHeldOffline  QueueHold = "offline"
	QueueHeldUnknown  QueueHold = "unknown"
	QueueHeldRefused  QueueHold = "refused"
	QueueHeldRetrying QueueHold = "retrying"
	QueueHeldBehind   QueueHold = "behind"
	QueueHeldUnloaded QueueHold = "notLoaded"
	// QueueHeldHostUnreachable is a send to a paired host's session that the
	// host never received; it is replayed with its key when the host returns.
	QueueHeldHostUnreachable QueueHold = "hostUnreachable"
)

// queueTargetStatus is the target's last observed status, as the daemon
// drains by it. A target the catalog no longer holds, or that discovery
// retired, is offline: its stored status and turn are the last observation,
// not a running process. A departed Claude session with a process id is the
// exception, since the daemon resumes it to drain its queue.
const queueTargetStatus = `CASE WHEN s.id IS NOT NULL AND (s.status='offline' OR NOT EXISTS (SELECT 1 FROM catalog_sessions cs WHERE cs.session_id=s.id)) THEN CASE WHEN s.surface='claude' AND s.pid>0 THEN 'idle' ELSE 'offline' END ELSE COALESCE(CASE WHEN sr.turn_observed=1 THEN sr.last_status END,s.status,'unknown') END`

// queueToPeerSocket matches an agent's message to a Claude session with a
// native peer socket, which the socket delivers whatever the turn is doing.
const queueToPeerSocket = `%[1]s.author!='person' AND EXISTS (SELECT 1 FROM sessions ps WHERE ps.id=%[1]s.session_id AND ps.surface='claude' AND ps.transport='uds')`

// queueSteerRow matches a message a busy turn can take as a steer under the
// live policy: one that asks to steer, or that makes no choice while the
// policy steers and is not bound for a peer socket.
func queueSteerRow(alias string, mode deliverypolicy.Mode) string {
	choice := `%[1]s.busy_delivery='steer'`
	if mode == deliverypolicy.Steer {
		choice = `(%[1]s.busy_delivery='steer' OR %[1]s.busy_delivery='' AND NOT (` + queueToPeerSocket + `))`
	}
	return fmt.Sprintf(choice+` AND %[1]s.operation='message' AND %[1]s.model='' AND (%[1]s.turn_options='' OR %[1]s.turn_options='{}')`, alias)
}

// queueHoldCase derives why a pending message is held by the rules the
// daemon drains by. A drain claims only the earliest open message of a
// session, or of its steerable messages while a turn runs, so a message
// behind an earlier one is held by it. A busy turn holds a message but not a
// steer into it when the target can steer. A Claude peer socket takes a peer
// message whether the turn runs or waits on its user. An unloaded conversation is
// drained only when Codex Desktop can load it. A message whose last attempt
// the target refused to start waits out its backoff as refused. Its first
// argument is the current time.
func queueHoldCase(canSteer bool, mode deliverypolicy.Mode) string {
	earliest := `(SELECT MIN(m.id) FROM message_queue m WHERE m.session_id=q.session_id AND m.status IN ('pending','inflight')%s)`
	steer := ""
	if canSteer {
		steer = `
		WHEN ` + queueTargetStatus + `='busy' AND ` + queueSteerRow("q", mode) + ` THEN CASE WHEN q.id!=` + fmt.Sprintf(earliest, " AND "+queueSteerRow("m", mode)) + ` THEN 'behind' WHEN q.refused=1 AND q.available_at_ms>?1 THEN 'refused' WHEN q.available_at_ms>?1 THEN 'retrying' ELSE '' END`
	}
	return `CASE WHEN q.status!='pending' THEN ''
		WHEN q.host_request!='' THEN 'hostUnreachable'
		WHEN ` + fmt.Sprintf(queueToPeerSocket, "q") + ` AND ` + fmt.Sprintf(queuePeerRow, "q") + ` THEN CASE WHEN q.id!=` + fmt.Sprintf(earliest, "") + ` THEN 'behind' WHEN q.refused=1 AND q.available_at_ms>?1 THEN 'refused' WHEN q.available_at_ms>?1 THEN 'retrying' ELSE '' END
		WHEN COALESCE(sr.waiting_id,'')!='' THEN 'waiting'` + steer + `
		WHEN ` + queueTargetStatus + `='busy' THEN 'busy'
		WHEN ` + queueTargetStatus + `='offline' THEN 'offline'
		WHEN ` + queueTargetStatus + ` NOT IN ('idle','notLoaded') THEN 'unknown'
		WHEN ` + queueTargetStatus + `='notLoaded' AND NOT (COALESCE(s.surface,'')='codex' AND COALESCE(s.transport,'')='desktop') THEN 'notLoaded'
		WHEN q.id!=` + fmt.Sprintf(earliest, "") + ` THEN 'behind'
		WHEN q.refused=1 AND q.available_at_ms>?1 THEN 'refused'
		WHEN q.available_at_ms>?1 THEN 'retrying'
		ELSE '' END`
}

// queueRowSelect reads queue rows with the hold reason for a target that can
// steer and for one that cannot under the live policy, and the target facts
// that decide which.
func queueRowSelect(mode deliverypolicy.Mode) string {
	return `SELECT q.id,q.session_id,q.message,q.model,q.source_session_id,q.turn_options,q.status,q.attempts,q.last_error,q.queued_at,q.dead_at_ms,q.evidence,q.operation,q.busy_delivery,
	` + queueHoldCase(true, mode) + `,
	` + queueHoldCase(false, mode) + `,
	COALESCE(s.surface,''),COALESCE(s.transport,''),COALESCE(s.bridge,''),COALESCE(s.runtime_kind,'')
	FROM message_queue q LEFT JOIN sessions s ON s.id=q.session_id LEFT JOIN session_runtime sr ON sr.session_id=q.session_id`
}

type queueRowScanner interface{ Scan(...any) error }

// scanQueueRow picks the hold for a target that can steer when the adapter
// that carries the target's runtime can steer into it; a target no adapter
// carries cannot.
func scanQueueRow(scanner queueRowScanner, adapters surface.Adapters, now time.Time) (QueueRow, error) {
	var row QueueRow
	var evidence, targetSurface, transport, bridge, runtimeKind string
	var steerHold, otherHold QueueHold
	if err := scanner.Scan(&row.ID, &row.SessionID, &row.Message, &row.Model, &row.SourceSessionID, &row.TurnOptions, &row.Status, &row.Attempts, &row.LastError, &row.QueuedAt, &row.DeadAt, &evidence, &row.Operation, &row.BusyDelivery, &steerHold, &otherHold, &targetSurface, &transport, &bridge, &runtimeKind); err != nil {
		return QueueRow{}, err
	}
	target := &surface.Session{ID: row.SessionID, Surface: surface.SurfaceKind(targetSurface), Transport: transport, Bridge: bridge, RuntimeKind: surface.RuntimeKind(runtimeKind)}
	row.HeldBy = otherHold
	if adapter, err := adapters.For(target); err == nil && surface.EffectiveCapabilities(adapter, target).Steer {
		row.HeldBy = steerHold
	}
	row.Historical = queueRowIsHistorical(row, now)
	row.Evidence = queueEvidence(row, surface.DeliveryEvidence(evidence))
	return row, nil
}

func (r *Registry) ListQueue(includeDelivered bool, adapters surface.Adapters) ([]QueueRow, error) {
	now := r.now()
	mode, err := r.busyMode()
	if err != nil {
		return nil, err
	}
	query := queueRowSelect(mode)
	args := []any{now.UnixMilli()}
	if !includeDelivered {
		query += ` WHERE q.status NOT IN ('delivered','canceled') AND (q.status!='dead' OR q.dead_at_ms>?)`
		args = append(args, now.Add(-deadReviewWindow).UnixMilli())
	}
	query += ` ORDER BY q.id`
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]QueueRow, 0)
	for rows.Next() {
		row, err := scanQueueRow(rows, adapters, now)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *Registry) QueueItem(id int64, adapters surface.Adapters) (*QueueRow, error) {
	now := r.now()
	mode, err := r.busyMode()
	if err != nil {
		return nil, err
	}
	row, err := scanQueueRow(r.read.QueryRow(queueRowSelect(mode)+` WHERE q.id=?`, now.UnixMilli(), id), adapters, now)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func queueRowIsHistorical(row QueueRow, now time.Time) bool {
	if row.Status == "delivered" || row.Status == "canceled" {
		return true
	}
	return row.Status == "dead" && row.DeadAt <= now.Add(-deadReviewWindow).UnixMilli()
}

func queueEvidence(row QueueRow, recorded surface.DeliveryEvidence) surface.DeliveryEvidence {
	if recorded != "" {
		return recorded
	}
	if row.Status == "dead" && strings.HasPrefix(row.LastError, "delivery outcome is unknown") {
		return surface.EvidenceUnknown
	}
	return QueueStatusEvidence(row.Status)
}

// QueueStatusEvidence is the evidence a queue status implies on its own.
func QueueStatusEvidence(status string) surface.DeliveryEvidence {
	switch status {
	case "pending", "inflight":
		return surface.EvidenceQueued
	case "delivered":
		return surface.EvidenceDelivered
	case "canceled":
		return surface.EvidenceCanceled
	case "dead":
		return surface.EvidenceFailed
	default:
		return ""
	}
}

func (r *Registry) ListAttentionItems(includeResolved bool) ([]AttentionItem, error) {
	query := `SELECT id,session_id,queue_id,reason,requested_action,created_at,resolved_at,resolution FROM attention_items`
	if !includeResolved {
		query += ` WHERE resolved_at=''`
	}
	query += ` ORDER BY created_at DESC,id DESC`
	rows, err := r.read.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AttentionItem, 0)
	for rows.Next() {
		var item AttentionItem
		if err := rows.Scan(&item.ID, &item.SessionID, &item.QueueID, &item.Reason, &item.RequestedAction, &item.CreatedAt, &item.ResolvedAt, &item.Resolution); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// queueRefusedRow matches an open message whose last attempt the target
// refused to start.
const queueRefusedRow = `refused=1 AND status IN ('pending','inflight')`

// ReconcileAttentionItems materializes attention rows from queue state. It is
// called by queue state writers and the background scan, never by snapshot reads.
func (r *Registry) ReconcileAttentionItems(now time.Time) error {
	if open, err := r.readExists(`SELECT EXISTS(SELECT 1 FROM message_queue WHERE status='dead' OR ` + queueRefusedRow + `) OR EXISTS(SELECT 1 FROM attention_items WHERE resolved_at='')`); err != nil || !open {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action)
		SELECT session_id,id,
			CASE
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 404%' THEN 'Delivery target is unavailable'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 401%' THEN 'Claude needs to reconnect'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 403%' THEN 'Claude denied this delivery'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 400%' THEN 'Delivery request needs correction'
				WHEN last_error LIKE 'delivery outcome is unknown%' THEN 'Delivery outcome could not be confirmed'
				WHEN last_error LIKE 'delivery rejected [target missing]:%' THEN 'Delivery target is unavailable'
				WHEN last_error LIKE 'delivery rejected [authentication needed]:%' THEN 'Claude needs to reconnect'
				WHEN last_error LIKE 'delivery rejected [access denied]:%' THEN 'Claude denied this delivery'
				WHEN last_error LIKE 'delivery rejected [invalid request]:%' THEN 'Delivery request needs correction'
				ELSE 'Delivery failed and needs a decision'
			END,
			CASE
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 404%' THEN 'Cancel and send this message to an available session'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 401%' THEN 'Reconnect Claude, then retry or cancel this message'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 403%' THEN 'Check Claude access, then retry or cancel this message'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 400%' THEN 'Correct this message, then retry or cancel it'
				WHEN last_error LIKE 'delivery rejected [target missing]:%' THEN 'Cancel and send this message to an available session'
				WHEN last_error LIKE 'delivery rejected [authentication needed]:%' THEN 'Reconnect Claude, then retry or cancel this message'
				WHEN last_error LIKE 'delivery rejected [access denied]:%' THEN 'Check Claude access, then retry or cancel this message'
				WHEN last_error LIKE 'delivery rejected [invalid request]:%' THEN 'Correct this message, then retry or cancel it'
				ELSE 'Retry or cancel this message'
			END
		FROM message_queue WHERE status='dead' AND dead_at_ms>?
		ON CONFLICT(queue_id) DO UPDATE SET
			reason=excluded.reason,
			requested_action=excluded.requested_action
		WHERE attention_items.resolved_at=''`, now.Add(-deadReviewWindow).UnixMilli())
	if err != nil {
		return err
	}
	// A message refused again after its refusal cleared reopens its item.
	_, err = tx.Exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action)
		SELECT session_id,id,'The target refused to start','Fix what the target reported; the message is delivered on its next attempt, or cancel it'
		FROM message_queue WHERE ` + queueRefusedRow + `
		ON CONFLICT(queue_id) DO UPDATE SET
			reason=excluded.reason,
			requested_action=excluded.requested_action,
			created_at=CASE WHEN attention_items.resolved_at!='' THEN datetime('now') ELSE attention_items.created_at END,
			resolved_at='',
			resolution=''`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE attention_items SET
		resolved_at=datetime('now'),
		resolution=COALESCE((SELECT CASE WHEN status='dead' AND dead_at_ms<=? THEN 'history' WHEN status='pending' THEN 'retrying' WHEN status='delivered' THEN 'delivered' WHEN status='canceled' THEN 'canceled' ELSE status END FROM message_queue WHERE id=attention_items.queue_id),'removed')
		WHERE resolved_at='' AND NOT EXISTS (SELECT 1 FROM message_queue WHERE id=attention_items.queue_id AND (status='dead' AND dead_at_ms>? OR `+queueRefusedRow+`))`, now.Add(-deadReviewWindow).UnixMilli(), now.Add(-deadReviewWindow).UnixMilli())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// RetryMessage sends a dead message again. Its delivery intent goes back to
// queued as a new attempt, so the earlier failure leaves the problem list and
// a later outcome is reported again.
func (r *Registry) RetryMessage(id int64) error {
	var sessionID, message string
	_ = r.db.QueryRow(`SELECT session_id,message FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message)
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='pending',attempts=0,deferrals=0,last_error='',available_at_ms=0,inflight_at_ms=0,dead_at_ms=0,delivered=0,updated_at=datetime('now') WHERE id=? AND status='dead'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not dead-lettered", id)
	}
	if err := retryQueuedDeliveryIntent(tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "retry", SessionID: sessionID, QueueID: id, Message: message, Result: "scheduled"})
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) CancelMessage(id int64) error {
	var sessionID, message string
	_ = r.db.QueryRow(`SELECT session_id,message FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message)
	res, err := r.db.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE id=? AND status IN ('pending','dead')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not pending or dead-lettered", id)
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "removed from delivery queue"})
	return r.ReconcileAttentionItems(r.now())
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
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE session_id=? AND status='pending'`, sessionID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, entry := range pending {
		_ = r.RecordHistory(entry)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := r.ReconcileAttentionItems(r.now()); err != nil {
		return count, err
	}
	return count, nil
}

func (r *Registry) ClaimNextMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	return r.claimNextMessage(sessionID, now, "")
}

func (r *Registry) ClaimNextSteerMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	return r.claimNextMessage(sessionID, now, "steer")
}

// ClaimNextPeerMessage claims the session's next queued message while a turn
// runs, when that message can ride a Claude peer socket.
func (r *Registry) ClaimNextPeerMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	return r.claimNextMessage(sessionID, now, claimPeer)
}

// claimPeer selects the session's next queued message only when it is a
// peer message: a Claude peer socket delivers it to a running turn, so
// nothing holds it there. A message with a per-message choice (an explicit
// queue or steer), a person's turn and a message that needs per-message
// options keep their own handling.
const claimPeer = "peer"

// queuePeerRow matches a message a Claude peer socket takes during a turn.
const queuePeerRow = `%[1]s.operation='message' AND %[1]s.author!='person' AND %[1]s.busy_delivery='' AND %[1]s.model='' AND (%[1]s.turn_options='' OR %[1]s.turn_options='{}') AND (%[1]s.uploads='' OR %[1]s.uploads='[]')`

// claimCandidateFilter selects the session's next queued message, the one a
// claim acts on.
func claimCandidateFilter(sessionID, busyDelivery string, mode deliverypolicy.Mode) (string, []any) {
	filter := `session_id=? AND host_request='' AND status IN ('pending','inflight') AND NOT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent'))`
	args := []any{sessionID, sessionID}
	if busyDelivery == claimPeer {
		filter += ` AND id=(SELECT MIN(m.id) FROM message_queue m WHERE m.session_id=? AND m.host_request='' AND m.status IN ('pending','inflight')) AND ` + fmt.Sprintf(queuePeerRow, "message_queue")
		args = append(args, sessionID)
	} else if busyDelivery == "steer" {
		filter += ` AND ` + queueSteerRow("message_queue", mode)
	}
	return filter + ` ORDER BY id LIMIT 1`, args
}

func claimInflightExpired(status string, operation QueueOperation, inflightAt int64, now time.Time) bool {
	inflightTimeout := time.Minute
	if operation != QueueOperationMessage {
		inflightTimeout = 10 * time.Minute
	}
	return status == "inflight" && inflightAt > 0 && inflightAt < now.Add(-inflightTimeout).UnixMilli()
}

// claimNeedsWrite reports whether a claim of this candidate changes it: an
// in-flight delivery that timed out, or a pending message that is available.
func claimNeedsWrite(status string, operation QueueOperation, availableAt, inflightAt int64, now time.Time) bool {
	return claimInflightExpired(status, operation, inflightAt, now) || status == "pending" && availableAt <= now.UnixMilli()
}

func (r *Registry) claimNextMessage(sessionID string, now time.Time, busyDelivery string) (*QueuedMessage, error) {
	if err := r.ReconcileAttentionItems(now); err != nil {
		return nil, err
	}
	mode, err := r.busyMode()
	if err != nil {
		return nil, err
	}
	filter, args := claimCandidateFilter(sessionID, busyDelivery, mode)
	var peekStatus string
	var peekOperation QueueOperation
	var peekAvailableAt, peekInflightAt int64
	err = r.read.QueryRow(`SELECT status,available_at_ms,inflight_at_ms,operation FROM message_queue WHERE `+filter, args...).Scan(&peekStatus, &peekAvailableAt, &peekInflightAt, &peekOperation)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !claimNeedsWrite(peekStatus, peekOperation, peekAvailableAt, peekInflightAt, now) {
		return nil, nil
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
	err = tx.QueryRow(`SELECT id,session_id,message,model,source_session_id,turn_options,attempts,relay_hops,status,available_at_ms,inflight_at_ms,operation,busy_delivery,author,uploads FROM message_queue WHERE `+filter, args...).Scan(&item.ID, &item.SessionID, &item.Message, &item.Model, &item.SourceSessionID, &item.TurnOptions, &item.Attempts, &item.RelayHops, &status, &availableAt, &inflightAt, &item.Operation, &item.BusyDelivery, &item.Author, &item.Uploads)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if claimInflightExpired(status, item.Operation, inflightAt, now) {
		if err := markQueuedDeliveryIntent(tx, item.ID, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), uncertainDeliveryError, item.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, r.ReconcileAttentionItems(now)
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
	res, err := r.db.Exec(`UPDATE message_queue SET refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, surface.EvidenceDelivered, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) AckMessageWithRelayHops(id int64, sessionID string, relayHops int) error {
	return r.AckMessageWithEvidence(id, sessionID, relayHops, surface.EvidenceDelivered, "")
}

func (r *Registry) AckMessageWithEvidence(id int64, sessionID string, relayHops int, evidence surface.DeliveryEvidence, providerKey string) error {
	if evidence == "" {
		return fmt.Errorf("delivery evidence is required")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND session_id=? AND status='inflight'`, evidence, id, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentSent, evidence, providerKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,relay_hops,turn_observed,updated_at) VALUES(?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET relay_hops=excluded.relay_hops,turn_observed=1,updated_at=datetime('now')`, sessionID, relayHops); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(r.now())
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
		available = now.Add(QueueRetryBackoff * time.Duration(1<<shift))
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	deadAt := int64(0)
	if status == "dead" {
		deadAt = now.UnixMilli()
	}
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status=?,dead_at_ms=?,last_error=?,available_at_ms=?,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, status, deadAt, message, available.UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 && status == "dead" {
		if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(now)
}

// DeferMessage returns a claim that provably did not reach the target, such as
// a busy refusal or an unavailable transport. The claim's attempt is given
// back so only real delivery errors spend the retry budget; deferrals keep
// their own count for backoff. A deferral the target refused starts a refusal,
// or continues the one in progress, and the first deferral of a refusal tells
// an agent sender why its message waits. Refusals of a message are numbered
// for its whole life, so a late acknowledgement of one never counts for
// another. DeferMessage returns the refusal's number while it still needs
// announcing to the user, and 0 otherwise; MarkRefusalAnnounced records that
// it was.
func (r *Registry) DeferMessage(id int64, cause error, now time.Time) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var deferrals, refusal, announced int64
	var wasRefused bool
	if err := tx.QueryRow(`SELECT deferrals,refused,refusal,refusal_announced FROM message_queue WHERE id=?`, id).Scan(&deferrals, &wasRefused, &refusal, &announced); err != nil {
		return 0, err
	}
	shift := min(deferrals, 6)
	message := "delivery did not start"
	if cause != nil {
		message = cause.Error()
	}
	refused := surface.IsDeliveryRefused(cause)
	began := refused && !wasRefused
	if began {
		refusal++
	}
	res, err := tx.Exec(`UPDATE message_queue SET status='pending',attempts=MAX(attempts-1,0),deferrals=deferrals+1,last_error=?,available_at_ms=?,inflight_at_ms=0,refused=?,refusal=?,updated_at=datetime('now') WHERE id=? AND status='inflight'`, message, now.Add(QueueRetryBackoff*time.Duration(1<<shift)).UnixMilli(), refused, refusal, id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, tx.Commit()
	}
	if began {
		if err := queueDeliveryRefusalNotice(tx, id, refusal, message); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if !refused || announced == refusal {
		refusal = 0
	}
	return refusal, r.ReconcileAttentionItems(now)
}

// MarkRefusalAnnounced records that the user was told about refusal, once both
// notifications were attempted, so a daemon stopped before then announces it
// on the next refused attempt. A refusal that has given way to a later one is
// left as it is.
func (r *Registry) MarkRefusalAnnounced(id, refusal int64) error {
	_, err := r.db.Exec(`UPDATE message_queue SET refusal_announced=? WHERE id=? AND refusal=?`, refusal, id, refusal)
	return err
}

func (r *Registry) DeadLetterUnknown(id int64, cause error) error {
	message := uncertainDeliveryError
	if cause != nil {
		message = fmt.Sprintf("delivery outcome is unknown: %s; retry explicitly if the target did not receive it", cause)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deadLetterInflight(tx, id, message, r.now()); err != nil {
		return err
	}
	if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) DeadLetterMessage(id int64, cause error) (bool, error) {
	message := "delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := deadLetterInflight(tx, id, message, r.now()); err != nil {
		return false, err
	}
	notified, err := finishQueuedDeliveryIntent(tx, id, message)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if err := r.ReconcileAttentionItems(r.now()); err != nil {
		return notified, err
	}
	return notified, nil
}

func deadLetterInflight(tx *sql.Tx, id int64, message string, now time.Time) error {
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), message, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	return nil
}

func (r *Registry) RecoverInflight(before time.Time) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,updated_at=datetime('now') WHERE status=? AND queue_id IN (SELECT id FROM message_queue WHERE status='inflight' AND inflight_at_ms<? AND host_request='')`, DeliveryIntentUnknown, surface.EvidenceUnknown, DeliveryIntentQueued, before.UnixMilli()); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE status='inflight' AND inflight_at_ms<? AND host_request=''`, r.now().UnixMilli(), uncertainDeliveryError, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if n > 0 {
		if err := r.ReconcileAttentionItems(before); err != nil {
			return n, err
		}
	}
	return n, nil
}

type RuntimeState struct {
	LastStatus        surface.SessionStatus
	ActiveTurnID      string
	CompletedTurnID   string
	RelayHops         int
	NotificationArmed bool
	UpdatedAt         time.Time
}

func (r *Registry) RuntimeState(sessionID string) (RuntimeState, bool, error) {
	var state RuntimeState
	var status, updatedAt string
	var notificationArmed int
	err := r.read.QueryRow(`SELECT last_status,active_turn_id,completed_turn_id,relay_hops,notification_armed,updated_at FROM session_runtime WHERE session_id=? AND turn_observed=1`, sessionID).Scan(&status, &state.ActiveTurnID, &state.CompletedTurnID, &state.RelayHops, &notificationArmed, &updatedAt)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.LastStatus = surface.SessionStatus(status)
	state.NotificationArmed = notificationArmed != 0
	if updatedAt != "" {
		state.UpdatedAt, err = time.ParseInLocation("2006-01-02 15:04:05", updatedAt, time.UTC)
		if err != nil {
			return state, false, fmt.Errorf("parse runtime state timestamp: %w", err)
		}
	}
	return state, true, nil
}

func (r *Registry) MarkDeliveryStarted(sessionID, activeTurnID, completedTurnID string) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,notification_armed,turn_observed,updated_at) VALUES(?,?,?,?,1,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,completed_turn_id=CASE WHEN session_runtime.turn_observed=1 THEN session_runtime.completed_turn_id ELSE excluded.completed_turn_id END,notification_armed=1,turn_observed=1,updated_at=datetime('now')`, sessionID, string(surface.StatusBusy), activeTurnID, completedTurnID)
	return err
}

func (r *Registry) SaveRuntimeState(sessionID string, observation surface.TurnObservation) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,turn_observed,updated_at) VALUES(?,?,?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,completed_turn_id=excluded.completed_turn_id,turn_observed=1,relay_hops=CASE WHEN excluded.completed_turn_id != session_runtime.completed_turn_id THEN 0 ELSE session_runtime.relay_hops END,updated_at=datetime('now')`, sessionID, string(observation.Status), observation.ActiveTurnID, observation.CompletedTurnID)
	return err
}

// SessionWaiting returns the question a session was last seen waiting on.
func (r *Registry) SessionWaiting(sessionID string) (*surface.Waiting, error) {
	waiting, _, err := r.WaitingState(sessionID)
	return waiting, err
}

// WaitingState returns the recorded question and whether the user has been
// told about it.
func (r *Registry) WaitingState(sessionID string) (*surface.Waiting, bool, error) {
	return scanWaiting(r.read.QueryRow(`SELECT waiting_kind,waiting_id,waiting_since_ms,waiting_announced,waiting_answerable FROM session_runtime WHERE session_id=?`, sessionID))
}

func scanWaiting(row *sql.Row) (*surface.Waiting, bool, error) {
	var kind, id string
	var sinceMS int64
	var announced, answerable bool
	err := row.Scan(&kind, &id, &sinceMS, &announced, &answerable)
	if err == sql.ErrNoRows || err == nil && id == "" {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &surface.Waiting{Kind: surface.WaitingKind(kind), ID: id, Since: time.UnixMilli(sinceMS).UTC(), Answerable: answerable}, announced, nil
}

// WaitingRecord is the result of recording what a session waits on.
type WaitingRecord struct {
	// Waiting is the recorded question, which keeps when it was first seen.
	Waiting *surface.Waiting
	// Announce reports that the user has not been told about it yet.
	Announce bool
	// Changed reports that the record now names a different question or none.
	Changed bool
	// Replaced is the question the record named before a change, and
	// ReplacedAnnounced whether the user was told about it.
	Replaced          *surface.Waiting
	ReplacedAnnounced bool
}

// RecordWaiting stores the question a session waits on. An unchanged state
// is answered from the read connection, so a pass over settled sessions
// writes nothing.
func (r *Registry) RecordWaiting(sessionID string, waiting *surface.Waiting, now time.Time) (WaitingRecord, error) {
	current, announced, err := r.WaitingState(sessionID)
	if err != nil {
		return WaitingRecord{}, err
	}
	if surface.SameWaiting(current, waiting) {
		return WaitingRecord{Waiting: current, Announce: current != nil && !announced}, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return WaitingRecord{}, err
	}
	defer tx.Rollback()
	stored, announced, err := scanWaiting(tx.QueryRow(`SELECT waiting_kind,waiting_id,waiting_since_ms,waiting_announced,waiting_answerable FROM session_runtime WHERE session_id=?`, sessionID))
	if err != nil {
		return WaitingRecord{}, err
	}
	if surface.SameWaiting(stored, waiting) {
		return WaitingRecord{Waiting: stored, Announce: stored != nil && !announced}, tx.Commit()
	}
	if waiting == nil {
		if _, err := tx.Exec(`UPDATE session_runtime SET waiting_kind='',waiting_id='',waiting_since_ms=0,waiting_announced=0,waiting_answerable=0 WHERE session_id=?`, sessionID); err != nil {
			return WaitingRecord{}, err
		}
		return WaitingRecord{Changed: true, Replaced: stored, ReplacedAnnounced: announced}, tx.Commit()
	}
	next := *waiting
	if next.Since.IsZero() {
		next.Since = now.UTC()
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,waiting_kind,waiting_id,waiting_since_ms,waiting_announced,waiting_answerable,updated_at) SELECT ?,?,?,?,0,?,datetime('now') WHERE EXISTS(SELECT 1 FROM sessions WHERE id=?)
		ON CONFLICT(session_id) DO UPDATE SET waiting_kind=excluded.waiting_kind,waiting_id=excluded.waiting_id,waiting_since_ms=excluded.waiting_since_ms,waiting_announced=0,waiting_answerable=excluded.waiting_answerable`, sessionID, string(next.Kind), next.ID, next.Since.UnixMilli(), next.Answerable, sessionID); err != nil {
		return WaitingRecord{}, err
	}
	return WaitingRecord{Waiting: &next, Announce: true, Changed: true, Replaced: stored, ReplacedAnnounced: announced}, tx.Commit()
}

// MarkWaitingAnnounced records that the user was told about question id. A
// question replaced in the meantime is left to be announced.
func (r *Registry) MarkWaitingAnnounced(sessionID, id string) error {
	_, err := r.db.Exec(`UPDATE session_runtime SET waiting_announced=1 WHERE session_id=? AND waiting_id=?`, sessionID, id)
	return err
}

func (r *Registry) ClearCompletionNotification(sessionID string) error {
	_, err := r.db.Exec(`UPDATE session_runtime SET notification_armed=0 WHERE session_id=?`, sessionID)
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
	rows, err := r.read.Query(`
		SELECT DISTINCT s.id,s.surface
		FROM sessions s
		WHERE s.host_id='' AND s.id IN (
			SELECT from_session FROM routes WHERE active=1
			UNION SELECT to_session FROM routes WHERE active=1
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
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var source string
	if err := tx.QueryRow(`SELECT from_session FROM routes WHERE id=?`, id).Scan(&source); err != nil {
		return fmt.Errorf("relay route %d does not exist: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM routes WHERE id=?`, id); err != nil {
		return err
	}
	key, err := relaySourceKeyOf(tx, source)
	if err != nil && !errors.Is(err, errNoRelayTranscript) {
		return err
	}
	if err == nil {
		orphaned := `source_key=? AND NOT EXISTS(SELECT 1 FROM routes JOIN sessions s ON s.id=routes.from_session WHERE ` + relaySourceKey + `=?)`
		for _, table := range []string{"relay_sources", "relay_events"} {
			if _, err := tx.Exec(`DELETE FROM `+table+` WHERE `+orphaned, key, key); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type RouteRow struct {
	Handoff      string `json:"handoff"`
	EventCursor  int64  `json:"eventCursor"`
	PendingCount int64  `json:"pendingCount"`
	LastGoalKey  string `json:"-"`
	LastError    string `json:"lastError,omitempty"`
	ID           int64  `json:"id"`
	FromSession  string `json:"fromSession"`
	ToSession    string `json:"toSession"`
	Pattern      string `json:"pattern"`
	Once         bool   `json:"once"`
	Active       bool   `json:"active"`
	FireCount    int64  `json:"fireCount"`
	LastFiredAt  string `json:"lastFiredAt,omitempty"`
}

func (r *Registry) ListRoutes() ([]RouteRow, error) {
	rows, err := r.read.Query(`SELECT r.id, r.from_session, r.to_session, r.pattern, r.once_only, r.active,
		COUNT(d.completion_id), COALESCE(MAX(d.delivered_at), ''), r.handoff, r.event_cursor, r.last_goal_key, r.last_error, (SELECT COUNT(*) FROM relay_events e JOIN sessions s ON s.id=r.from_session WHERE e.source_key=` + relaySourceKey + ` AND e.id>r.event_cursor)
		FROM routes r LEFT JOIN relay_deliveries d ON d.route_id = r.id
		GROUP BY r.id, r.from_session, r.to_session, r.pattern, r.once_only, r.active
		ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteRow
	for rows.Next() {
		var r RouteRow
		var once, active int
		if err := rows.Scan(&r.ID, &r.FromSession, &r.ToSession, &r.Pattern, &once, &active, &r.FireCount, &r.LastFiredAt, &r.Handoff, &r.EventCursor, &r.LastGoalKey, &r.LastError, &r.PendingCount); err != nil {
			return nil, err
		} else {
			r.Once = once != 0
			r.Active = active != 0
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (r *Registry) DeactivateRoute(id int64) error {
	res, err := r.db.Exec(`UPDATE routes SET active=0 WHERE id=? AND active=1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("active relay route %d does not exist", id)
	}
	return nil
}

type AliasRow struct {
	Name      string
	SessionID string
}

func (r *Registry) ListAliases() ([]AliasRow, error) {
	rows, err := r.read.Query(`SELECT name, session_id FROM aliases ORDER BY name`)
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
	rows, err := r.read.Query(`
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
	rows, err := r.read.Query(`
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
	err = r.read.QueryRow(`SELECT surface, name, cwd FROM sessions WHERE id = ?`, id).Scan(&surface, &name, &cwd)
	return
}

// sessionSelect reads one session with what its catalog row shows it
// waiting on and running in the background, so every API that returns a session agrees with the catalog.
// status is the column expression for its status.
func sessionSelect(status string) string {
	return `SELECT s.id,s.surface,s.name,s.cwd,s.pid,` + status + `,s.transcript,s.has_local,s.source,s.transport,s.runtime_kind,s.configured_model,s.bridge,s.forked_from,s.last_active_ms,COALESCE(sr.runtime_launcher,''),sr.runtime_location,COALESCE(sr.runtime_focusable,0),COALESCE(cs.waiting_on,X''),COALESCE(cs.background_tasks,X''),COALESCE(cs.usage_limit,X''),` + sessionSubagentSelect
}

const sessionJoins = ` LEFT JOIN session_runtime sr ON sr.session_id=s.id LEFT JOIN catalog_sessions cs ON cs.session_id=s.id`

func scanSession(scanner interface{ Scan(...any) error }) (surface.Session, error) {
	var session surface.Session
	var kind, status string
	var hasLocal int
	var lastActiveMS int64
	var launcher string
	var location, waitingOn, background, usageLimit []byte
	var focusable int
	var subagent subagentColumns
	if err := scanner.Scan(append([]any{&session.ID, &kind, &session.Name, &session.Cwd, &session.PID, &status, &session.Transcript, &hasLocal, &session.Source, &session.Transport, &session.RuntimeKind, &session.ConfiguredModel, &session.Bridge, &session.ForkedFrom, &lastActiveMS, &launcher, &location, &focusable, &waitingOn, &background, &usageLimit}, subagent.targets()...)...); err != nil {
		return surface.Session{}, err
	}
	session.Surface = surface.SurfaceKind(kind)
	session.Status = surface.SessionStatus(status)
	session.HasLocal = hasLocal != 0
	if lastActiveMS > 0 {
		session.LastActive = time.UnixMilli(lastActiveMS)
	}
	subagent.apply(&session)
	runtime, err := runtimeFromColumns(launcher, location, focusable)
	if err != nil {
		return surface.Session{}, err
	}
	session.Runtime = runtime
	if session.WaitingOn, err = decodeCatalogWaiting(waitingOn); err != nil {
		return surface.Session{}, err
	}
	if session.BackgroundTasks, err = decodeCatalogBackground(background); err != nil {
		return surface.Session{}, err
	}
	if session.UsageLimit, err = decodeCatalogUsageLimit(usageLimit); err != nil {
		return surface.Session{}, err
	}
	return session, nil
}

// decodeCatalogWaiting reads a catalog row's waiting_on column.
func decodeCatalogWaiting(raw []byte) (*surface.Waiting, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var waiting *surface.Waiting
	if err := json.Unmarshal(raw, &waiting); err != nil {
		return nil, fmt.Errorf("parse catalog waiting state: %w", err)
	}
	return waiting, nil
}

// decodeCatalogBackground reads a catalog row's background_tasks column.
func decodeCatalogBackground(raw []byte) (*surface.BackgroundTasks, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var background *surface.BackgroundTasks
	if err := json.Unmarshal(raw, &background); err != nil {
		return nil, fmt.Errorf("parse catalog background tasks: %w", err)
	}
	return background, nil
}

// encodeCatalogBackground writes a session's background tasks for its catalog
// row; none is the empty value.
func encodeCatalogBackground(background *surface.BackgroundTasks) ([]byte, error) {
	if background == nil {
		return []byte{}, nil
	}
	return json.Marshal(background)
}

// decodeCatalogUsageLimit reads a catalog row's usage_limit column.
func decodeCatalogUsageLimit(raw []byte) (*surface.UsageLimit, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var limit *surface.UsageLimit
	if err := json.Unmarshal(raw, &limit); err != nil {
		return nil, fmt.Errorf("parse catalog usage limit: %w", err)
	}
	return limit, nil
}

// encodeCatalogUsageLimit writes a session's usage limit for its catalog row;
// none is the empty value.
func encodeCatalogUsageLimit(limit *surface.UsageLimit) ([]byte, error) {
	if limit == nil {
		return []byte{}, nil
	}
	return json.Marshal(limit)
}

func scanSessions(rows *sql.Rows) ([]surface.Session, error) {
	defer rows.Close()
	results := make([]surface.Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *Registry) Session(id string) (*surface.Session, error) {
	session, err := scanSession(r.read.QueryRow(sessionSelect(`s.status`)+` FROM sessions s`+sessionJoins+` WHERE s.id = ?`, id))
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *Registry) SearchSessions(kind surface.SurfaceKind, query string, limit int) ([]surface.Session, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []surface.Session{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	pattern := "%" + escapeLike(query) + "%"
	rows, err := r.read.Query(sessionSelect(`s.status`)+`
		FROM sessions s`+sessionJoins+`
		WHERE s.surface=? AND s.host_id='' AND (s.id LIKE ? ESCAPE '\' OR s.name LIKE ? ESCAPE '\' OR s.cwd LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM aliases a WHERE a.session_id=s.id AND a.name LIKE ? ESCAPE '\'))
		ORDER BY s.last_active_ms DESC, s.updated_at DESC, s.id LIMIT ?`, string(kind), pattern, pattern, pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	return scanSessions(rows)
}

// ListSessions reports a session the catalog no longer holds as offline: its
// stored status is the last observation, not a running process.
func (r *Registry) ListSessions(limit int) ([]surface.Session, error) {
	query := sessionSelect(`CASE WHEN cs.session_id IS NOT NULL THEN s.status ELSE ? END`) + ` FROM sessions s` + sessionJoins + ` WHERE s.host_id='' ORDER BY s.last_active_ms DESC, s.updated_at DESC, s.id`
	args := []any{string(surface.StatusOffline)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanSessions(rows)
}

func (r *Registry) SessionUpdatedBefore(id string, before time.Time) (bool, error) {
	var stale bool
	err := r.read.QueryRow(`SELECT updated_at < datetime(?,'unixepoch') FROM sessions WHERE id=?`, before.Unix(), id).Scan(&stale)
	return stale, err
}

func (r *Registry) ReverseAlias(sessionID string) (string, error) {
	var name string
	err := r.read.QueryRow(`SELECT name FROM aliases WHERE session_id = ?`, sessionID).Scan(&name)
	return name, err
}

func (r *Registry) RemoveAlias(name string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if coordinated, err := coordinatorNamedTx(tx, name); err != nil {
		return err
	} else if coordinated {
		return CoordinatorAliasError{Name: name}
	}
	res, err := tx.Exec(`DELETE FROM aliases WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("alias %q does not exist", name)
	}
	return tx.Commit()
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

func (r *Registry) ChannelExists(channelName string) (bool, error) {
	var exists bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels WHERE name=?)`, channelName).Scan(&exists)
	return exists, err
}

func (r *Registry) RenameChannel(channelName, newName string) error {
	if exists, err := r.ChannelExists(newName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("channel #%s already exists", newName)
	}
	res, err := r.db.Exec(`UPDATE channels SET name=? WHERE name=?`, newName, channelName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("channel %q does not exist", channelName)
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
