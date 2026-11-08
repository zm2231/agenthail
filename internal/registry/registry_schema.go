package registry

import (
	_ "modernc.org/sqlite"
)

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
	host_id TEXT NOT NULL DEFAULT '',
	pinned_at_ms INTEGER NOT NULL DEFAULT 0, stopped_at_ms INTEGER NOT NULL DEFAULT 0,
	snoozed_at_ms INTEGER NOT NULL DEFAULT 0, snoozed_until_ms INTEGER NOT NULL DEFAULT 0, snooze_wake TEXT NOT NULL DEFAULT '',
	ended INTEGER NOT NULL DEFAULT 0
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
	at_ms INTEGER NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	acknowledged_at_ms INTEGER NOT NULL DEFAULT 0,
	announced_at_ms INTEGER NOT NULL DEFAULT 0,
	notify_sender INTEGER NOT NULL DEFAULT 0,
	person_notified_at_ms INTEGER NOT NULL DEFAULT 0
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
	refusal_announced INTEGER NOT NULL DEFAULT 0,
	person_name TEXT NOT NULL DEFAULT ''
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
	waiting_answerable INTEGER NOT NULL DEFAULT 0,
	held_completion_turn_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS limit_resumes (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	turn_id TEXT NOT NULL,
	account_key TEXT NOT NULL DEFAULT '',
	refused_at_ms INTEGER NOT NULL DEFAULT 0,
	resets_at_ms INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT 'waiting',
	attempts INTEGER NOT NULL DEFAULT 0,
	updated_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_origins (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	via TEXT NOT NULL,
	creator_session_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS session_ceilings (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	ceiling TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_codex_config (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	overrides TEXT NOT NULL DEFAULT '{}',
	updated_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS pending_ceilings (
	credential_id INTEGER PRIMARY KEY REFERENCES session_credentials(id) ON DELETE CASCADE,
	ceiling TEXT NOT NULL
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
	error TEXT NOT NULL DEFAULT '',
	operation TEXT NOT NULL DEFAULT '',
	send_route TEXT NOT NULL DEFAULT '',
	bridge TEXT NOT NULL DEFAULT ''
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
	revoked_at TEXT NOT NULL DEFAULT '',
	browser INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS paired_devices_active ON paired_devices(revoked_at, created_at DESC);
CREATE TABLE IF NOT EXISTS device_push_targets (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	installation_id TEXT NOT NULL,
	credential TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS push_outcomes (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	event_type TEXT NOT NULL DEFAULT '',
	attempted_at TEXT NOT NULL DEFAULT '',
	succeeded_at TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	failed_at TEXT NOT NULL DEFAULT '',
	received_at TEXT NOT NULL DEFAULT '',
	alerts TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS push_sends (
	push_id TEXT PRIMARY KEY,
	device_id TEXT NOT NULL REFERENCES paired_devices(id) ON DELETE CASCADE,
	event_type TEXT NOT NULL DEFAULT '',
	sent_at TEXT NOT NULL,
	confirmed INTEGER NOT NULL DEFAULT 0,
	received_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS push_sends_device ON push_sends(device_id, sent_at DESC);
CREATE TABLE IF NOT EXISTS device_connection_drops (
	device_id TEXT NOT NULL REFERENCES paired_devices(id) ON DELETE CASCADE,
	drop_id TEXT NOT NULL,
	host TEXT NOT NULL,
	via TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL,
	started_at TEXT NOT NULL,
	recovered_at TEXT NOT NULL,
	recovered_by TEXT NOT NULL DEFAULT '',
	first_error TEXT NOT NULL,
	last_error TEXT NOT NULL,
	elapsed_ms INTEGER,
	attempts INTEGER NOT NULL,
	shown TEXT NOT NULL,
	since_foreground_ms INTEGER,
	went_background INTEGER NOT NULL DEFAULT 0,
	network TEXT NOT NULL DEFAULT '',
	vpn INTEGER NOT NULL DEFAULT 0,
	expensive INTEGER NOT NULL DEFAULT 0,
	constrained INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (device_id, drop_id)
);
CREATE INDEX IF NOT EXISTS device_connection_drops_recent ON device_connection_drops(device_id, started_at DESC);
CREATE TABLE IF NOT EXISTS browser_push_subscriptions (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	endpoint TEXT NOT NULL,
	p256dh TEXT NOT NULL,
	auth TEXT NOT NULL,
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
	payload_codec INTEGER NOT NULL DEFAULT 0,
	payload_role TEXT NOT NULL DEFAULT '',
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
	pruned_seq INTEGER NOT NULL DEFAULT 0,
	seed_status TEXT NOT NULL DEFAULT 'unknown',
	seed_seq INTEGER NOT NULL DEFAULT 0,
	seed_identity TEXT NOT NULL DEFAULT '',
	history_before INTEGER NOT NULL DEFAULT -1,
	derived_version INTEGER NOT NULL DEFAULT 0,
	transcript_checkpoint TEXT NOT NULL DEFAULT '',
	swept_seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS session_journal_bodies (
	ref TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	body BLOB NOT NULL,
	created_at TEXT NOT NULL,
	written_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS session_journal_bodies_session ON session_journal_bodies(session_id);
CREATE TABLE IF NOT EXISTS session_journal_body_tail (
	ref TEXT NOT NULL,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	start INTEGER NOT NULL,
	chunk BLOB NOT NULL,
	PRIMARY KEY (ref, start)
);
CREATE INDEX IF NOT EXISTS session_journal_body_tail_session ON session_journal_body_tail(session_id);
CREATE TABLE IF NOT EXISTS session_credentials (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
	pending_ref TEXT UNIQUE,
	pending_expires_at_ms INTEGER NOT NULL DEFAULT 0,
	claimed_session_id TEXT UNIQUE,
	token_hash TEXT NOT NULL UNIQUE,
	created_at_ms INTEGER NOT NULL,
	last_used_at_ms INTEGER NOT NULL DEFAULT 0,
	idle_expires_at_ms INTEGER NOT NULL,
	max_expires_at_ms INTEGER NOT NULL,
	revoked_at_ms INTEGER NOT NULL DEFAULT 0,
	revoked_reason TEXT NOT NULL DEFAULT '',
	CHECK ((session_id IS NULL) <> (pending_ref IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS session_credentials_live ON session_credentials(session_id) WHERE revoked_at_ms=0;
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
	failed_from TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS delivery_intents_target
	ON delivery_intents(target_session_id, id DESC);
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
	catalog_seq INTEGER NOT NULL DEFAULT 0,
	catalog_retired_clock INTEGER NOT NULL DEFAULT -1
);
CREATE TABLE IF NOT EXISTS tasks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	child_session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,
	task TEXT NOT NULL,
	state TEXT NOT NULL DEFAULT 'running',
	result TEXT NOT NULL DEFAULT '',
	result_kind TEXT NOT NULL DEFAULT '',
	result_turn_id TEXT NOT NULL DEFAULT '',
	latest TEXT NOT NULL DEFAULT '',
	latest_turn_id TEXT NOT NULL DEFAULT '',
	latest_at_ms INTEGER NOT NULL DEFAULT 0,
	last_turn_id TEXT NOT NULL DEFAULT '',
	last_state TEXT NOT NULL DEFAULT '',
	last_text TEXT NOT NULL DEFAULT '',
	stalled INTEGER NOT NULL DEFAULT 0,
	wake INTEGER NOT NULL DEFAULT 0,
	created_at_ms INTEGER NOT NULL,
	settled_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tasks_running ON tasks(child_session_id) WHERE state='running';
CREATE INDEX IF NOT EXISTS tasks_parent ON tasks(parent_session_id);
CREATE TABLE IF NOT EXISTS work_records (
	worktree_path TEXT PRIMARY KEY,
	session_id TEXT NOT NULL DEFAULT '',
	repo_root TEXT NOT NULL,
	branch TEXT NOT NULL,
	base_ref TEXT NOT NULL,
	base_commit TEXT NOT NULL,
	state TEXT NOT NULL,
	settled_reason TEXT NOT NULL DEFAULT '',
	cleanup TEXT NOT NULL DEFAULT '',
	cleanup_reason TEXT NOT NULL DEFAULT '',
	kept_paths TEXT NOT NULL DEFAULT '',
	created_at_ms INTEGER NOT NULL,
	settled_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS work_records_session ON work_records(session_id) WHERE session_id<>'';
CREATE TABLE IF NOT EXISTS session_prs (
	session_id TEXT NOT NULL,
	repo TEXT NOT NULL,
	number INTEGER NOT NULL,
	url TEXT NOT NULL,
	source TEXT NOT NULL,
	unlinked INTEGER NOT NULL DEFAULT 0,
	watch INTEGER NOT NULL DEFAULT 1,
	title TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	head_sha TEXT NOT NULL DEFAULT '',
	mergeable TEXT NOT NULL DEFAULT '',
	review TEXT NOT NULL DEFAULT '',
	checks TEXT NOT NULL DEFAULT '',
	failed_checks TEXT NOT NULL DEFAULT '',
	passed_head TEXT NOT NULL DEFAULT '',
	conflict_head TEXT NOT NULL DEFAULT '',
	remarks_through_ms INTEGER NOT NULL DEFAULT 0,
	remark_ids TEXT NOT NULL DEFAULT '',
	wakes INTEGER NOT NULL DEFAULT 0,
	comment_wakes INTEGER NOT NULL DEFAULT 0,
	read_failures INTEGER NOT NULL DEFAULT 0,
	last_read_at_ms INTEGER NOT NULL DEFAULT 0,
	linked_at_ms INTEGER NOT NULL,
	PRIMARY KEY (session_id, repo, number)
);
CREATE INDEX IF NOT EXISTS session_prs_watched ON session_prs(repo) WHERE watch=1 AND unlinked=0;
CREATE TABLE IF NOT EXISTS catalog_retired_version (
	id INTEGER PRIMARY KEY CHECK (id=1),
	version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS catalog_presentation_version (
	id INTEGER PRIMARY KEY CHECK (id=1),
	version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS host_queue_dirty (
	session_id TEXT PRIMARY KEY
) WITHOUT ROWID;
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
	turn_stop BLOB NOT NULL DEFAULT '',
	settings BLOB NOT NULL DEFAULT '',
	current INTEGER NOT NULL DEFAULT 0,
	current_reason TEXT NOT NULL DEFAULT '',
	retired INTEGER NOT NULL DEFAULT 0
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
CREATE TABLE IF NOT EXISTS project_links (
	joined TEXT PRIMARY KEY,
	survivor TEXT NOT NULL,
	linked INTEGER NOT NULL,
	changed_at_ms INTEGER NOT NULL,
	origin_host TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS session_archive (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	archived_at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS session_keep_alive (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	attempts INTEGER NOT NULL DEFAULT 0,
	first_attempt_ms INTEGER NOT NULL DEFAULT 0,
	next_attempt_ms INTEGER NOT NULL DEFAULT 0,
	healthy_since_ms INTEGER NOT NULL DEFAULT 0,
	failed_at_ms INTEGER NOT NULL DEFAULT 0,
	announced INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS idle_compactions (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	window_ms INTEGER NOT NULL,
	queue_id INTEGER NOT NULL DEFAULT 0,
	request_uuid TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, window_ms)
);
CREATE INDEX IF NOT EXISTS idle_compactions_queue ON idle_compactions(queue_id) WHERE queue_id!=0;
CREATE TABLE IF NOT EXISTS turn_ends (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	started_at_ms INTEGER NOT NULL,
	ended_at_ms INTEGER NOT NULL DEFAULT 0,
	turn_id TEXT NOT NULL DEFAULT '',
	completed_turn_id TEXT NOT NULL DEFAULT '',
	needs TEXT NOT NULL DEFAULT '',
	headline TEXT NOT NULL DEFAULT '',
	asks_json TEXT NOT NULL DEFAULT 'null',
	next_json TEXT NOT NULL DEFAULT 'null',
	source TEXT NOT NULL DEFAULT '',
	routed_to TEXT NOT NULL DEFAULT '',
	answered_at_ms INTEGER NOT NULL DEFAULT 0,
	answer_status TEXT NOT NULL DEFAULT '',
	answer_message_id TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, started_at_ms)
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
CREATE TABLE IF NOT EXISTS ledger_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	project_key TEXT NOT NULL,
	type TEXT NOT NULL,
	payload BLOB NOT NULL,
	actor_session_id TEXT NOT NULL DEFAULT '',
	actor_host_id TEXT NOT NULL DEFAULT '',
	owner_seq INTEGER NOT NULL DEFAULT 0,
	origin_seq INTEGER NOT NULL DEFAULT 0,
	ref TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS ledger_events_project ON ledger_events(project_key, seq);
CREATE UNIQUE INDEX IF NOT EXISTS ledger_events_ref ON ledger_events(project_key, ref) WHERE ref != '';
CREATE UNIQUE INDEX IF NOT EXISTS ledger_events_origin ON ledger_events(project_key, actor_host_id, origin_seq) WHERE origin_seq > 0;
CREATE TABLE IF NOT EXISTS ledger_grants (
	project_key TEXT NOT NULL,
	session_id TEXT NOT NULL,
	since_ms INTEGER NOT NULL,
	PRIMARY KEY (project_key, session_id)
);
CREATE INDEX IF NOT EXISTS ledger_grants_session ON ledger_grants(session_id);
CREATE TABLE IF NOT EXISTS removed_project_ledgers (
	workspace_root TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	removed_at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS project_moves (
	from_key TEXT PRIMARY KEY,
	to_key TEXT NOT NULL,
	changed_at_ms INTEGER NOT NULL,
	origin_host TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_move_refusals (
	from_key TEXT PRIMARY KEY,
	to_key TEXT NOT NULL,
	reason TEXT NOT NULL,
	at_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS repository_keys (
	workspace_root TEXT PRIMARY KEY,
	project_key TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ledger_state (
	project_key TEXT PRIMARY KEY,
	owner_host_id TEXT NOT NULL DEFAULT '',
	owner_cursor INTEGER NOT NULL DEFAULT 0,
	deadline_ms INTEGER NOT NULL DEFAULT 0,
	freeze_version TEXT NOT NULL DEFAULT ''
);
`
