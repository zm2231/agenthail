-- schemaVersion 40

-- index aliases_session_id
CREATE UNIQUE INDEX aliases_session_id ON aliases(session_id);

-- index attention_items_open
CREATE INDEX attention_items_open ON attention_items(resolved_at, created_at DESC, id DESC);

-- index catalog_events_created
CREATE INDEX catalog_events_created ON catalog_events(created_at DESC, seq DESC);

-- index daemon_events_created
CREATE INDEX daemon_events_created ON daemon_events(created_at DESC, id DESC);

-- index delivery_history_created_at
CREATE INDEX delivery_history_created_at ON delivery_history(created_at DESC, id DESC);

-- index delivery_intents_provider_key
CREATE UNIQUE INDEX delivery_intents_provider_key
	ON delivery_intents(target_session_id, provider_key) WHERE provider_key!='';

-- index delivery_intents_sender_status
CREATE INDEX delivery_intents_sender_status
	ON delivery_intents(sender_session_id, status, id DESC);

-- index delivery_intents_uuid
CREATE UNIQUE INDEX delivery_intents_uuid ON delivery_intents(uuid);

-- index device_pairings_expires
CREATE INDEX device_pairings_expires ON device_pairings(expires_at);

-- index message_queue_delivery_key
CREATE UNIQUE INDEX message_queue_delivery_key
		ON message_queue(delivery_key) WHERE delivery_key!='';

-- index paired_devices_active
CREATE INDEX paired_devices_active ON paired_devices(revoked_at, created_at DESC);

-- index relay_events_source
CREATE INDEX relay_events_source ON relay_events(source_key,id);

-- index session_journal_bodies_session
CREATE INDEX session_journal_bodies_session ON session_journal_bodies(session_id);

-- index session_journal_position
CREATE UNIQUE INDEX session_journal_position ON session_journal(session_id, position);

-- index session_journal_prompts
CREATE INDEX session_journal_prompts
	ON session_journal(session_id, payload_at DESC, seq DESC, kind, payload_role, payload_origin, payload_body, payload_item_id, payload_turn_id, payload_ts, payload_truncated, body_ref)
	WHERE kind IN ('message','text') AND payload_role='user';

-- index session_journal_prompts_at
CREATE INDEX session_journal_prompts_at
	ON session_journal(payload_at DESC, session_id DESC, seq DESC, kind, payload_role, payload_origin, payload_body, payload_item_id, payload_turn_id, payload_ts, payload_truncated, body_ref)
	WHERE kind IN ('message','text') AND payload_role='user';

-- index session_journal_provider_key
CREATE UNIQUE INDEX session_journal_provider_key
	ON session_journal(session_id, provider_key) WHERE provider_key!='';

-- index session_moves_conversation
CREATE INDEX session_moves_conversation ON session_moves(conversation, role, state);

-- index sessions_recent
CREATE INDEX sessions_recent ON sessions(last_active_ms DESC, updated_at DESC, id);

-- table action_receipts
CREATE TABLE action_receipts (
	idempotency_key TEXT PRIMARY KEY,
	request_hash TEXT NOT NULL,
	status TEXT NOT NULL,
	receipt BLOB NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

-- table alias_reservations
CREATE TABLE alias_reservations (
	name TEXT PRIMARY KEY,
	token TEXT NOT NULL,
	expires_at_ms INTEGER NOT NULL
);

-- table aliases
CREATE TABLE aliases (
	name TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table attention_items
CREATE TABLE attention_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	queue_id INTEGER NOT NULL UNIQUE REFERENCES message_queue(id) ON DELETE CASCADE,
	reason TEXT NOT NULL,
	requested_action TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	resolved_at TEXT NOT NULL DEFAULT '',
	resolution TEXT NOT NULL DEFAULT ''
);

-- table browser_push_subscriptions
CREATE TABLE browser_push_subscriptions (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	endpoint TEXT NOT NULL,
	p256dh TEXT NOT NULL,
	auth TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table catalog_events
CREATE TABLE catalog_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	dedupe_key TEXT NOT NULL UNIQUE,
	type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);

-- table catalog_sessions
CREATE TABLE catalog_sessions (
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

-- table catalog_state
CREATE TABLE catalog_state (
	id INTEGER PRIMARY KEY CHECK (id=1),
	host_epoch TEXT NOT NULL,
	projection_clock INTEGER NOT NULL DEFAULT 0
);

-- table catalog_surfaces
CREATE TABLE catalog_surfaces (
	surface TEXT PRIMARY KEY,
	health TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	observed_at TEXT NOT NULL
);

-- table channel_members
CREATE TABLE channel_members (
	channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	PRIMARY KEY (channel_id, session_id)
);

-- table channels
CREATE TABLE channels (
	id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table coordinator_history
CREATE TABLE coordinator_history (
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

-- table coordinator_replaced
CREATE TABLE coordinator_replaced (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	project_id TEXT NOT NULL
);

-- table coordinators
CREATE TABLE coordinators (
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

-- table daemon_events
CREATE TABLE daemon_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL DEFAULT '',
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);

-- table delivery_history
CREATE TABLE delivery_history (
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
	operation TEXT NOT NULL DEFAULT ''
);

-- table delivery_intents
CREATE TABLE delivery_intents (
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

-- table device_pairings
CREATE TABLE device_pairings (
	id TEXT PRIMARY KEY,
	secret_hash TEXT NOT NULL UNIQUE,
	requested_name TEXT NOT NULL DEFAULT '',
	scopes TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	consumed_at TEXT NOT NULL DEFAULT '',
	device_id TEXT NOT NULL DEFAULT ''
);

-- table device_push_targets
CREATE TABLE device_push_targets (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	installation_id TEXT NOT NULL,
	credential TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table host_identity
CREATE TABLE host_identity (
	id INTEGER PRIMARY KEY CHECK (id=1),
	host_id TEXT NOT NULL,
	name TEXT NOT NULL
);

-- table hosts
CREATE TABLE hosts (
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

-- table launcher_pending
CREATE TABLE launcher_pending (
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

-- table limit_resumes
CREATE TABLE limit_resumes (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	turn_id TEXT NOT NULL,
	account_key TEXT NOT NULL DEFAULT '',
	refused_at_ms INTEGER NOT NULL DEFAULT 0,
	resets_at_ms INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT 'waiting',
	attempts INTEGER NOT NULL DEFAULT 0,
	updated_at_ms INTEGER NOT NULL DEFAULT 0
);

-- table message_queue
CREATE TABLE message_queue (
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

-- table paired_devices
CREATE TABLE paired_devices (
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

-- table projects
CREATE TABLE projects (
	id TEXT PRIMARY KEY,
	workspace_root TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	added INTEGER NOT NULL DEFAULT 0,
	project_key TEXT NOT NULL DEFAULT '',
	repository BLOB NOT NULL DEFAULT '',
	created_at_ms INTEGER NOT NULL DEFAULT 0,
	archived_at_ms INTEGER NOT NULL DEFAULT 0
);

-- table relay_deliveries
CREATE TABLE relay_deliveries (
	route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
	completion_id TEXT NOT NULL,
	delivered_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (route_id, completion_id)
);

-- table relay_events
CREATE TABLE relay_events (
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

-- table relay_provenance
CREATE TABLE relay_provenance (
    queue_id INTEGER PRIMARY KEY REFERENCES message_queue(id) ON DELETE CASCADE,
    input_id TEXT NOT NULL DEFAULT '',
    completion_id TEXT NOT NULL DEFAULT ''
);

-- table relay_sources
CREATE TABLE relay_sources (
    source_key TEXT PRIMARY KEY,
    cursor TEXT NOT NULL
);

-- table routes
CREATE TABLE routes (
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

-- table session_archive
CREATE TABLE session_archive (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	archived_at_ms INTEGER NOT NULL
);

-- table session_context
CREATE TABLE session_context (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	fingerprint TEXT NOT NULL,
	start_key TEXT NOT NULL DEFAULT '',
	delivered_at_ms INTEGER NOT NULL
);

-- table session_journal
CREATE TABLE session_journal (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	position INTEGER NOT NULL DEFAULT 0,
	kind TEXT NOT NULL,
	provider_key TEXT NOT NULL DEFAULT '',
	payload BLOB NOT NULL,
	observed_at TEXT NOT NULL,
	bytes INTEGER NOT NULL,
	body_ref TEXT NOT NULL DEFAULT '', payload_role TEXT GENERATED ALWAYS AS (json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.role')) VIRTUAL, payload_origin TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.origin'),'')) VIRTUAL, payload_body TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.body'),'')) VIRTUAL, payload_item_id TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.itemId'),'')) VIRTUAL, payload_turn_id TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.turnId'),'')) VIRTUAL, payload_ts TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.ts'),'')) VIRTUAL, payload_truncated INTEGER GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.truncated'),0)) VIRTUAL, payload_at REAL GENERATED ALWAYS AS (COALESCE(julianday(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.ts')),julianday(observed_at),0)) VIRTUAL,
	PRIMARY KEY (session_id, seq)
);

-- table session_journal_bodies
CREATE TABLE session_journal_bodies (
	ref TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	body BLOB NOT NULL,
	created_at TEXT NOT NULL
);

-- table session_journal_state
CREATE TABLE session_journal_state (
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
	transcript_checkpoint TEXT NOT NULL DEFAULT ''
);

-- table session_moves
CREATE TABLE session_moves (
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

-- table session_origins
CREATE TABLE session_origins (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	via TEXT NOT NULL,
	creator_session_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table session_rekeys
CREATE TABLE session_rekeys (
	old_id TEXT PRIMARY KEY, new_id TEXT NOT NULL,
	rekeyed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table session_runtime
CREATE TABLE session_runtime (
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

-- table sessions
CREATE TABLE sessions (
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

-- table signin_bindings
CREATE TABLE signin_bindings (
	consumer TEXT PRIMARY KEY,
	profile_id TEXT NOT NULL REFERENCES signin_profiles(id) ON DELETE CASCADE
);

-- table signin_off
CREATE TABLE signin_off (
	consumer TEXT PRIMARY KEY
);

-- table signin_profiles
CREATE TABLE signin_profiles (
	id TEXT PRIMARY KEY,
	label TEXT NOT NULL UNIQUE,
	browser TEXT NOT NULL,
	profile TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(browser, profile)
);

-- trigger catalog_sessions_current_insert
CREATE TRIGGER catalog_sessions_current_insert AFTER INSERT ON catalog_sessions
BEGIN
	UPDATE catalog_sessions SET current=CASE WHEN json_valid(NEW.projection_fingerprint) THEN COALESCE(json_extract(NEW.projection_fingerprint,'$.current'),0) ELSE 0 END,
		current_reason=CASE WHEN json_valid(NEW.projection_fingerprint) THEN COALESCE(json_extract(NEW.projection_fingerprint,'$.currentReason'),'') ELSE '' END WHERE session_id=NEW.session_id;
END;

-- trigger catalog_sessions_current_update
CREATE TRIGGER catalog_sessions_current_update AFTER UPDATE OF projection_fingerprint ON catalog_sessions
BEGIN
	UPDATE catalog_sessions SET current=CASE WHEN json_valid(NEW.projection_fingerprint) THEN COALESCE(json_extract(NEW.projection_fingerprint,'$.current'),0) ELSE 0 END,
		current_reason=CASE WHEN json_valid(NEW.projection_fingerprint) THEN COALESCE(json_extract(NEW.projection_fingerprint,'$.currentReason'),'') ELSE '' END WHERE session_id=NEW.session_id;
END;

-- trigger message_queue_catalog_delete
CREATE TRIGGER message_queue_catalog_delete AFTER DELETE ON message_queue
WHEN OLD.status IN ('pending','inflight') AND NOT EXISTS(SELECT 1 FROM sessions WHERE id=OLD.session_id AND host_id<>'')
BEGIN
	INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at)
	VALUES('session.queue:'||OLD.session_id||':'||lower(hex(randomblob(12))),'session.queue',OLD.session_id,
		json_object('sessionId',OLD.session_id,'queueCount',(SELECT COUNT(*) FROM message_queue WHERE session_id=OLD.session_id AND status IN ('pending','inflight'))),
		strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;

-- trigger message_queue_catalog_insert
CREATE TRIGGER message_queue_catalog_insert AFTER INSERT ON message_queue
WHEN NEW.status IN ('pending','inflight') AND NOT EXISTS(SELECT 1 FROM sessions WHERE id=NEW.session_id AND host_id<>'')
BEGIN
	INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at)
	VALUES('session.queue:'||NEW.session_id||':'||lower(hex(randomblob(12))),'session.queue',NEW.session_id,
		json_object('sessionId',NEW.session_id,'queueCount',(SELECT COUNT(*) FROM message_queue WHERE session_id=NEW.session_id AND status IN ('pending','inflight'))),
		strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;

-- trigger message_queue_catalog_update
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

