package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func (r *Registry) migrate(allowUpgrade bool) error {
	if err := r.chooseVacuumForNewFile(); err != nil {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	timing := MigrationTiming{From: version, To: schemaVersion}
	switch {
	case version == schemaVersion:
		return nil
	case version > schemaVersion:
		return r.newerSchemaError(version)
	case version > 0 && version < schemaVersion && !allowUpgrade:
		return &DevUpgradeError{Path: r.diskPath(), Found: version, Target: schemaVersion}
	case version > 0 && version < schemaVersion:
		began := time.Now()
		if err := r.backUpBeforeMigration(version, schemaVersion); err != nil {
			return err
		}
		timing.Backup = time.Since(began)
	}
	began := time.Now()
	if err := migrateTx(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	timing.Migrate = time.Since(began)
	if version > 0 {
		began = time.Now()
		timing.CompressErr = compressMigrationBackups(r.diskPath())
		timing.Compress = time.Since(began)
		r.migration = &timing
	}
	return nil
}

// An existing file would need a VACUUM to change this, and that stalls writers for longer than busy_timeout on a large registry, so only a new file gets it.
func (r *Registry) chooseVacuumForNewFile() error {
	var pages int
	if err := r.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		return err
	}
	if pages > 0 {
		return nil
	}
	_, err := r.db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`)
	return err
}

// Migration is what opening this registry spent upgrading it, if it did.
func (r *Registry) Migration() (MigrationTiming, bool) {
	if r.migration == nil {
		return MigrationTiming{}, false
	}
	return *r.migration, true
}

func (r *Registry) newerSchemaError(found int) *NewerSchemaError {
	refusal := &NewerSchemaError{Found: found, Supported: schemaVersion, Path: r.diskPath()}
	if backup := existingMigrationBackup(refusal.Path, found); backup != "" {
		if schema := backupSchema(backup); schema > 0 && schema <= schemaVersion {
			refusal.Backup, refusal.BackupSchema = backup, schema
		}
	}
	return refusal
}

func migrateTx(tx *sql.Tx) error {
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if err := dropPresentationTriggers(tx); err != nil {
		return err
	}
	if err := setAsideReleasedProjectsTable(tx); err != nil {
		return err
	}
	if err := setAsideBoundOnlyCredentials(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if err := upgradeReleasedProjectsTable(tx); err != nil {
		return err
	}
	if err := dropCredentialPolicyColumns(tx); err != nil {
		return err
	}
	if err := upgradeBoundOnlyCredentials(tx); err != nil {
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
	if err := ensureColumn(tx, "session_journal_state", "swept_seq", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal", "payload_codec", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_journal_bodies", "written_seq", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "delivery_history", "operation", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	for _, column := range []string{"send_route", "bridge"} {
		if err := ensureColumn(tx, "delivery_history", column, `TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	for _, column := range []struct{ name, decl string }{
		{"reason", `TEXT NOT NULL DEFAULT ''`},
		{"acknowledged_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"announced_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"notify_sender", `INTEGER NOT NULL DEFAULT 0`},
		{"person_notified_at_ms", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(tx, "coordinator_history", column.name, column.decl); err != nil {
			return err
		}
	}
	if err := allowMovedCoordinatorHistory(tx); err != nil {
		return err
	}
	if err := ensureColumn(tx, "session_moves", "outcome_unknown_at", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "coordinators", "moved_from", `TEXT NOT NULL DEFAULT ''`); err != nil {
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
		{"person_name", `TEXT NOT NULL DEFAULT ''`},
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
		{"pinned_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"stopped_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"snoozed_at_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"snoozed_until_ms", `INTEGER NOT NULL DEFAULT 0`},
		{"snooze_wake", `TEXT NOT NULL DEFAULT ''`},
		{"ended", `INTEGER NOT NULL DEFAULT 0`},
		{"entrypoint", `TEXT NOT NULL DEFAULT ''`},
	} {
		if err := ensureColumn(tx, "sessions", column.name, column.decl); err != nil {
			return err
		}
	}
	if err := addAskRefs(tx); err != nil {
		return err
	}
	if err := moveTurnEndAsks(tx); err != nil {
		return err
	}
	if err := ensureJournalPromptIndexes(tx, version > 0 && version < 57); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS sessions_recent ON sessions(last_active_ms DESC, updated_at DESC, id)`); err != nil {
		return err
	}
	for _, column := range []struct{ name, decl string }{
		{"relay_hops", `INTEGER NOT NULL DEFAULT 0`},
		{"notification_armed", `INTEGER NOT NULL DEFAULT 0`},
		{"runtime_launcher", `TEXT NOT NULL DEFAULT ''`},
		{"runtime_location", `BLOB NOT NULL DEFAULT '{}'`},
		{"runtime_focusable", `INTEGER NOT NULL DEFAULT 0`},
		{"held_completion_turn_id", `TEXT NOT NULL DEFAULT ''`},
		{"notice_turn_id", `TEXT NOT NULL DEFAULT ''`},
		{"notice_outcome", `TEXT NOT NULL DEFAULT ''`},
		{"notice_request_id", `TEXT NOT NULL DEFAULT ''`},
		{"notice_at_ms", `INTEGER NOT NULL DEFAULT 0`},
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
	if err := ensureColumn(tx, "session_journal_state", "pruned_seq", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if version < 33 {
		if _, err := tx.Exec(`UPDATE session_journal SET position=seq WHERE position=0`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE session_journal_state SET pruned_seq=MAX(pruned_before,COALESCE((SELECT MIN(seq)-1 FROM session_journal WHERE session_journal.session_id=session_journal_state.session_id),0))`); err != nil {
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
	if err := ensureColumn(tx, "delivery_intents", "failed_from", `TEXT NOT NULL DEFAULT ''`); err != nil {
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
		{"turn_stop", `BLOB NOT NULL DEFAULT ''`},
		{"settings", `BLOB NOT NULL DEFAULT ''`},
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
	if err := ensureColumn(tx, "catalog_sessions", "retired", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(tx, "hosts", "catalog_retired_clock", `INTEGER NOT NULL DEFAULT -1`); err != nil {
		return err
	}
	if _, err := tx.Exec(catalogRetiredSchema); err != nil {
		return err
	}
	if _, err := tx.Exec(hostQueueDirtySchema); err != nil {
		return err
	}
	if version < 67 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE host_id<>''`); err != nil {
			return err
		}
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
	if err := ensureColumn(tx, "paired_devices", "browser", `INTEGER NOT NULL DEFAULT 0`); err != nil {
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
	if version < 34 {
		if err := splitOperator(tx); err != nil {
			return err
		}
	}
	if version < 48 {
		if _, err := tx.Exec(`UPDATE coordinator_history SET announced_at_ms=at_ms, acknowledged_at_ms=at_ms, person_notified_at_ms=at_ms WHERE action='replaced'`); err != nil {
			return err
		}
	}
	if version < 54 {
		if err := moveLedgerCaptureIntoTheLog(tx); err != nil {
			return err
		}
	}
	if version >= 1 && version < 59 {
		if _, err := tx.Exec(`UPDATE sessions SET pinned_at_ms=CAST(strftime('%s','now') AS INTEGER)*1000 WHERE pinned_at_ms=0 AND id IN (` + localLeadSessions + `)`); err != nil {
			return err
		}
	}
	if version < 68 {
		if _, err := tx.Exec(`UPDATE sessions SET ended=1 WHERE status='offline'`); err != nil {
			return err
		}
	}
	if err := createPresentationTriggers(tx); err != nil {
		return err
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

func catalogCurrentAssignment(row string) string {
	projection := row + ".projection_fingerprint"
	return `current=CASE WHEN json_valid(` + projection + `) THEN COALESCE(json_extract(` + projection + `,'$.current'),0) ELSE 0 END,
		current_reason=CASE WHEN json_valid(` + projection + `) THEN COALESCE(json_extract(` + projection + `,'$.currentReason'),'') ELSE '' END`
}

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

func backfillIntentUUIDs(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE delivery_intents SET uuid=` + sqlRandomUUID + ` WHERE uuid=''`)
	return err
}

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

func setAsideReleasedProjectsTable(tx *sql.Tx) error {
	released, err := columnExists(tx, "projects", "root_path")
	if err != nil || !released {
		return err
	}
	_, err = tx.Exec(`ALTER TABLE projects RENAME TO projects_released`)
	return err
}

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
	rows, err := tx.Query(`PRAGMA table_xinfo(` + table + `)`)
	if err != nil {
		return false, err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk, hidden int
		var columnName, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &pk, &hidden); err != nil {
			rows.Close()
			return false, err
		}
		if columnName == name {
			found = true
		}
	}
	return found, rows.Close()
}

// SQLite cannot change a CHECK in place, so the table is made again.
func allowMovedCoordinatorHistory(tx *sql.Tx) error {
	var definition string
	if err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='coordinator_history'`).Scan(&definition); err != nil {
		return err
	}
	if strings.Contains(definition, "'moved'") {
		return nil
	}
	const columns = `id,project_id,action,name,session_id,previous_name,previous_session_id,changed_by,at_ms,reason,acknowledged_at_ms,announced_at_ms,notify_sender,person_notified_at_ms`
	for _, step := range []string{
		`ALTER TABLE coordinator_history RENAME TO coordinator_history_unmoved`,
		schema,
		`INSERT INTO coordinator_history (` + columns + `) SELECT ` + columns + ` FROM coordinator_history_unmoved`,
		`DROP TABLE coordinator_history_unmoved`,
	} {
		if _, err := tx.Exec(step); err != nil {
			return err
		}
	}
	return nil
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

// moveLedgerCaptureIntoTheLog turns this host's own enable or disable of a
// project's ledger into a capture event, which replays to the ledger's owner
// like any other event this host wrote.
func moveLedgerCaptureIntoTheLog(tx *sql.Tx) error {
	exists, err := columnExists(tx, "ledger_state", "enabled")
	if err != nil || !exists {
		return err
	}
	for _, step := range []string{
		`INSERT INTO ledger_events(project_key,type,payload,actor_session_id,actor_host_id,owner_seq,origin_seq,ref,created_at)
		SELECT project_key,'capture',
			json_object('state',CASE enabled WHEN 1 THEN 'enabled' ELSE 'disabled' END,
				'at',strftime('%Y-%m-%dT%H:%M:%fZ',CASE WHEN enabled=1 AND since_ms>0 THEN since_ms ELSE CAST(strftime('%s','now') AS INTEGER)*1000 END/1000.0,'unixepoch')),
			'',COALESCE((SELECT host_id FROM host_identity WHERE id=1),''),0,0,'',
			strftime('%Y-%m-%dT%H:%M:%SZ',CASE WHEN enabled=1 AND since_ms>0 THEN since_ms ELSE CAST(strftime('%s','now') AS INTEGER)*1000 END/1000,'unixepoch')
		FROM ledger_state WHERE enabled IN (1,2) ORDER BY project_key`,
		`UPDATE ledger_events SET origin_seq=seq WHERE type='capture' AND origin_seq=0`,
		`ALTER TABLE ledger_state DROP COLUMN enabled`,
		`ALTER TABLE ledger_state DROP COLUMN since_ms`,
	} {
		if _, err := tx.Exec(step); err != nil {
			return err
		}
	}
	return nil
}
