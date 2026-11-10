-- schemaVersion 92

-- index aliases_session_id
CREATE UNIQUE INDEX aliases_session_id ON aliases(session_id);

-- index asks_open
CREATE INDEX asks_open ON asks(routed_to) WHERE state='open';

-- index asks_ref
CREATE UNIQUE INDEX asks_ref ON asks(session_id, origin, ref) WHERE ref<>'' AND state<>'withdrawn';

-- index asks_session
CREATE INDEX asks_session ON asks(session_id, created_at_ms);

-- index catalog_events_created
CREATE INDEX catalog_events_created ON catalog_events(created_at DESC, seq DESC);

-- index catalog_sessions_retired
CREATE INDEX catalog_sessions_retired ON catalog_sessions(projection_generation) WHERE retired=1;

-- index catalog_sessions_unretired
CREATE INDEX catalog_sessions_unretired ON catalog_sessions(session_id) WHERE retired=0;

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

-- index delivery_intents_status_updated
CREATE INDEX delivery_intents_status_updated
	ON delivery_intents(status, updated_at);

-- index delivery_intents_target
CREATE INDEX delivery_intents_target
	ON delivery_intents(target_session_id, id DESC);

-- index delivery_intents_uuid
CREATE UNIQUE INDEX delivery_intents_uuid ON delivery_intents(uuid);

-- index device_connection_drops_recent
CREATE INDEX device_connection_drops_recent ON device_connection_drops(device_id, started_at DESC);

-- index device_pairings_expires
CREATE INDEX device_pairings_expires ON device_pairings(expires_at);

-- index host_held_actions_open
CREATE INDEX host_held_actions_open ON host_held_actions(host_id, id) WHERE status IN ('pending','inflight');

-- index idle_compactions_queue
CREATE INDEX idle_compactions_queue ON idle_compactions(queue_id) WHERE queue_id!=0;

-- index ledger_events_origin
CREATE UNIQUE INDEX ledger_events_origin ON ledger_events(project_key, actor_host_id, origin_seq) WHERE origin_seq > 0;

-- index ledger_events_project
CREATE INDEX ledger_events_project ON ledger_events(project_key, seq);

-- index ledger_events_ref
CREATE UNIQUE INDEX ledger_events_ref ON ledger_events(project_key, ref) WHERE ref != '';

-- index ledger_grants_session
CREATE INDEX ledger_grants_session ON ledger_grants(session_id);

-- index message_queue_delivery_key
CREATE UNIQUE INDEX message_queue_delivery_key
		ON message_queue(delivery_key) WHERE delivery_key!='';

-- index paired_devices_active
CREATE INDEX paired_devices_active ON paired_devices(revoked_at, created_at DESC);

-- index push_sends_device
CREATE INDEX push_sends_device ON push_sends(device_id, sent_at DESC);

-- index relay_events_source
CREATE INDEX relay_events_source ON relay_events(source_key,id);

-- index session_credentials_live
CREATE UNIQUE INDEX session_credentials_live ON session_credentials(session_id) WHERE revoked_at_ms=0;

-- index session_journal_bodies_session
CREATE INDEX session_journal_bodies_session ON session_journal_bodies(session_id);

-- index session_journal_body_tail_session
CREATE INDEX session_journal_body_tail_session ON session_journal_body_tail(session_id);

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

-- index session_prs_watched
CREATE INDEX session_prs_watched ON session_prs(repo) WHERE watch=1 AND unlinked=0;

-- index sessions_parent
CREATE INDEX sessions_parent ON sessions(parent_session_id) WHERE parent_session_id<>'';

-- index sessions_recent
CREATE INDEX sessions_recent ON sessions(last_active_ms DESC, updated_at DESC, id);

-- index sessions_root
CREATE INDEX sessions_root ON sessions(root_session_id) WHERE root_session_id<>'';

-- index tasks_parent
CREATE INDEX tasks_parent ON tasks(parent_session_id);

-- index tasks_running
CREATE INDEX tasks_running ON tasks(child_session_id) WHERE state='running';

-- index trigger_runs_state
CREATE INDEX trigger_runs_state ON trigger_runs(state, trigger_name, id);

-- index work_records_session
CREATE UNIQUE INDEX work_records_session ON work_records(session_id) WHERE session_id<>'';

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

-- table asks
CREATE TABLE asks (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	origin TEXT NOT NULL,
	title TEXT NOT NULL,
	questions_json TEXT NOT NULL,
	routed_to TEXT NOT NULL,
	routed_at_ms INTEGER NOT NULL,
	route_json TEXT NOT NULL,
	state TEXT NOT NULL CHECK (state IN ('open','answered','withdrawn')),
	answer_json TEXT NOT NULL DEFAULT '',
	answered_by TEXT NOT NULL DEFAULT '',
	answered_via TEXT NOT NULL DEFAULT '',
	answered_at_ms INTEGER NOT NULL DEFAULT 0,
	applied_at_ms INTEGER NOT NULL DEFAULT 0,
	apply_status TEXT NOT NULL DEFAULT '',
	apply_id TEXT NOT NULL DEFAULT '',
	created_at_ms INTEGER NOT NULL,
	ref TEXT NOT NULL DEFAULT '',
	wait_kind TEXT NOT NULL DEFAULT '',
	answerable INTEGER NOT NULL DEFAULT 1,
	announced_at_ms INTEGER NOT NULL DEFAULT 0
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

-- table catalog_presentation_version
CREATE TABLE catalog_presentation_version (
	id INTEGER PRIMARY KEY CHECK (id=1),
	version INTEGER NOT NULL
);

-- table catalog_retired_version
CREATE TABLE catalog_retired_version (
	id INTEGER PRIMARY KEY CHECK (id=1),
	version INTEGER NOT NULL
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
	turn_stop BLOB NOT NULL DEFAULT '',
	settings BLOB NOT NULL DEFAULT '',
	current INTEGER NOT NULL DEFAULT 0,
	current_reason TEXT NOT NULL DEFAULT '',
	retired INTEGER NOT NULL DEFAULT 0
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
	action TEXT NOT NULL CHECK (action IN ('set','replaced','cleared','moved')),
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
	moved_from TEXT NOT NULL DEFAULT '',
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
	operation TEXT NOT NULL DEFAULT '',
	send_route TEXT NOT NULL DEFAULT '',
	bridge TEXT NOT NULL DEFAULT ''
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
	failed_from TEXT NOT NULL DEFAULT '',
	retries INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table device_connection_drops
CREATE TABLE device_connection_drops (
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

-- table host_held_actions
CREATE TABLE host_held_actions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	host_id TEXT NOT NULL,
	delivery_key TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	request BLOB NOT NULL,
	status TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 1,
	inflight_at_ms INTEGER NOT NULL DEFAULT 0,
	outcome TEXT NOT NULL DEFAULT '',
	created_at_ms INTEGER NOT NULL,
	updated_at_ms INTEGER NOT NULL
);

-- table host_identity
CREATE TABLE host_identity (
	id INTEGER PRIMARY KEY CHECK (id=1),
	host_id TEXT NOT NULL,
	name TEXT NOT NULL
);

-- table host_queue_dirty
CREATE TABLE host_queue_dirty (
	session_id TEXT PRIMARY KEY
) WITHOUT ROWID;

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
	catalog_seq INTEGER NOT NULL DEFAULT 0,
	catalog_retired_clock INTEGER NOT NULL DEFAULT -1
);

-- table idle_compactions
CREATE TABLE idle_compactions (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	window_ms INTEGER NOT NULL,
	queue_id INTEGER NOT NULL DEFAULT 0,
	request_uuid TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, window_ms)
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

-- table ledger_events
CREATE TABLE ledger_events (
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

-- table ledger_grants
CREATE TABLE ledger_grants (
	project_key TEXT NOT NULL,
	session_id TEXT NOT NULL,
	since_ms INTEGER NOT NULL,
	PRIMARY KEY (project_key, session_id)
);

-- table ledger_state
CREATE TABLE ledger_state (
	project_key TEXT PRIMARY KEY,
	owner_host_id TEXT NOT NULL DEFAULT '',
	owner_cursor INTEGER NOT NULL DEFAULT 0,
	deadline_ms INTEGER NOT NULL DEFAULT 0,
	freeze_version TEXT NOT NULL DEFAULT ''
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
	person_name TEXT NOT NULL DEFAULT ''
);

-- table move_dropped_relays
CREATE TABLE move_dropped_relays (
	move_id TEXT NOT NULL,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	relay_id INTEGER NOT NULL,
	to_session TEXT NOT NULL,
	at_ms INTEGER NOT NULL,
	PRIMARY KEY (move_id, relay_id)
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

-- table pending_ceilings
CREATE TABLE pending_ceilings (
	credential_id INTEGER PRIMARY KEY REFERENCES session_credentials(id) ON DELETE CASCADE,
	ceiling TEXT NOT NULL
);

-- table project_links
CREATE TABLE project_links (
	joined TEXT PRIMARY KEY,
	survivor TEXT NOT NULL,
	linked INTEGER NOT NULL,
	changed_at_ms INTEGER NOT NULL,
	origin_host TEXT NOT NULL
);

-- table project_move_refusals
CREATE TABLE project_move_refusals (
	from_key TEXT PRIMARY KEY,
	to_key TEXT NOT NULL,
	reason TEXT NOT NULL,
	at_ms INTEGER NOT NULL
);

-- table project_moves
CREATE TABLE project_moves (
	from_key TEXT PRIMARY KEY,
	to_key TEXT NOT NULL,
	changed_at_ms INTEGER NOT NULL,
	origin_host TEXT NOT NULL
);

-- table project_moves_waiting
CREATE TABLE project_moves_waiting (
	from_key TEXT PRIMARY KEY,
	to_key TEXT NOT NULL,
	changed_at_ms INTEGER NOT NULL,
	origin_host TEXT NOT NULL,
	workspace_root TEXT NOT NULL DEFAULT ''
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

-- table push_outcomes
CREATE TABLE push_outcomes (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	event_type TEXT NOT NULL DEFAULT '',
	attempted_at TEXT NOT NULL DEFAULT '',
	succeeded_at TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	failed_at TEXT NOT NULL DEFAULT '',
	received_at TEXT NOT NULL DEFAULT '',
	alerts TEXT NOT NULL DEFAULT ''
);

-- table push_sends
CREATE TABLE push_sends (
	push_id TEXT PRIMARY KEY,
	device_id TEXT NOT NULL REFERENCES paired_devices(id) ON DELETE CASCADE,
	event_type TEXT NOT NULL DEFAULT '',
	sent_at TEXT NOT NULL,
	confirmed INTEGER NOT NULL DEFAULT 0,
	received_at TEXT NOT NULL DEFAULT ''
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

-- table remote_channels
CREATE TABLE remote_channels (
	channel_name TEXT NOT NULL,
	home_host_id TEXT NOT NULL,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	PRIMARY KEY (session_id, home_host_id, channel_name)
);

-- table removed_project_ledgers
CREATE TABLE removed_project_ledgers (
	workspace_root TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	removed_at_ms INTEGER NOT NULL
);

-- table repository_keys
CREATE TABLE repository_keys (
	workspace_root TEXT PRIMARY KEY,
	project_key TEXT NOT NULL
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

-- table session_ceilings
CREATE TABLE session_ceilings (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	ceiling TEXT NOT NULL
);

-- table session_codex_config
CREATE TABLE session_codex_config (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	overrides TEXT NOT NULL DEFAULT '{}',
	updated_at_ms INTEGER NOT NULL DEFAULT 0
);

-- table session_context
CREATE TABLE session_context (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	fingerprint TEXT NOT NULL,
	start_key TEXT NOT NULL DEFAULT '',
	delivered_at_ms INTEGER NOT NULL
);

-- table session_credentials
CREATE TABLE session_credentials (
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
	body_ref TEXT NOT NULL DEFAULT '',
	payload_codec INTEGER NOT NULL DEFAULT 0,
	payload_role TEXT NOT NULL DEFAULT '', payload_origin TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.origin'),'')) VIRTUAL, payload_body TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.body'),'')) VIRTUAL, payload_item_id TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.itemId'),'')) VIRTUAL, payload_turn_id TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.turnId'),'')) VIRTUAL, payload_ts TEXT GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.ts'),'')) VIRTUAL, payload_truncated INTEGER GENERATED ALWAYS AS (COALESCE(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.truncated'),0)) VIRTUAL, payload_at REAL GENERATED ALWAYS AS (COALESCE(julianday(json_extract(CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END,'$.ts')),julianday(observed_at),0)) VIRTUAL,
	PRIMARY KEY (session_id, seq)
);

-- table session_journal_bodies
CREATE TABLE session_journal_bodies (
	ref TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	body BLOB NOT NULL,
	created_at TEXT NOT NULL,
	written_seq INTEGER NOT NULL DEFAULT 0
);

-- table session_journal_body_tail
CREATE TABLE session_journal_body_tail (
	ref TEXT NOT NULL,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	start INTEGER NOT NULL,
	chunk BLOB NOT NULL,
	PRIMARY KEY (ref, start)
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
	transcript_checkpoint TEXT NOT NULL DEFAULT '',
	swept_seq INTEGER NOT NULL DEFAULT 0
);

-- table session_keep_alive
CREATE TABLE session_keep_alive (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	attempts INTEGER NOT NULL DEFAULT 0,
	first_attempt_ms INTEGER NOT NULL DEFAULT 0,
	next_attempt_ms INTEGER NOT NULL DEFAULT 0,
	healthy_since_ms INTEGER NOT NULL DEFAULT 0,
	failed_at_ms INTEGER NOT NULL DEFAULT 0,
	announced INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT ''
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
	updated_at TEXT NOT NULL,
	outcome_unknown_at TEXT NOT NULL DEFAULT ''
);

-- table session_origins
CREATE TABLE session_origins (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	via TEXT NOT NULL,
	creator_session_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- table session_prs
CREATE TABLE session_prs (
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
	held_completion_turn_id TEXT NOT NULL DEFAULT '',
	notice_turn_id TEXT NOT NULL DEFAULT '',
	notice_outcome TEXT NOT NULL DEFAULT '',
	notice_request_id TEXT NOT NULL DEFAULT '',
	notice_at_ms INTEGER NOT NULL DEFAULT 0
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
	host_id TEXT NOT NULL DEFAULT '',
	pinned_at_ms INTEGER NOT NULL DEFAULT 0, stopped_at_ms INTEGER NOT NULL DEFAULT 0,
	snoozed_at_ms INTEGER NOT NULL DEFAULT 0, snoozed_until_ms INTEGER NOT NULL DEFAULT 0, snooze_wake TEXT NOT NULL DEFAULT '',
	ended INTEGER NOT NULL DEFAULT 0,
	entrypoint TEXT NOT NULL DEFAULT ''
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

-- table tasks
CREATE TABLE tasks (
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

-- table trigger_runs
CREATE TABLE trigger_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trigger_name TEXT NOT NULL,
	dedupe_key TEXT NOT NULL,
	session_id TEXT NOT NULL DEFAULT '',
	turn_id TEXT NOT NULL DEFAULT '',
	completion TEXT NOT NULL DEFAULT '{}',
	test INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	exit_code INTEGER NOT NULL DEFAULT 0,
	signal TEXT NOT NULL DEFAULT '',
	stdout TEXT NOT NULL DEFAULT '',
	stderr TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	queued_at_ms INTEGER NOT NULL,
	started_at_ms INTEGER NOT NULL DEFAULT 0,
	finished_at_ms INTEGER NOT NULL DEFAULT 0,
	UNIQUE (trigger_name, dedupe_key)
);

-- table turn_ends
CREATE TABLE turn_ends (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	started_at_ms INTEGER NOT NULL,
	ended_at_ms INTEGER NOT NULL DEFAULT 0,
	turn_id TEXT NOT NULL DEFAULT '',
	completed_turn_id TEXT NOT NULL DEFAULT '',
	headline TEXT NOT NULL DEFAULT '',
	next_json TEXT NOT NULL DEFAULT 'null',
	source TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, started_at_ms)
);

-- table work_records
CREATE TABLE work_records (
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

-- trigger catalog_presentation_version_aliases_delete
CREATE TRIGGER catalog_presentation_version_aliases_delete AFTER DELETE ON aliases
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_aliases_insert
CREATE TRIGGER catalog_presentation_version_aliases_insert AFTER INSERT ON aliases
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_aliases_update
CREATE TRIGGER catalog_presentation_version_aliases_update AFTER UPDATE OF created_at,name,session_id ON aliases WHEN (NEW.created_at IS NOT OLD.created_at OR NEW.name IS NOT OLD.name OR NEW.session_id IS NOT OLD.session_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_asks_delete
CREATE TRIGGER catalog_presentation_version_asks_delete AFTER DELETE ON asks
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_asks_insert
CREATE TRIGGER catalog_presentation_version_asks_insert AFTER INSERT ON asks
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_asks_update
CREATE TRIGGER catalog_presentation_version_asks_update AFTER UPDATE OF announced_at_ms,answer_json,answerable,answered_at_ms,answered_by,answered_via,applied_at_ms,apply_id,apply_status,created_at_ms,id,origin,questions_json,ref,route_json,routed_at_ms,routed_to,session_id,state,title,wait_kind ON asks WHEN (NEW.announced_at_ms IS NOT OLD.announced_at_ms OR NEW.answer_json IS NOT OLD.answer_json OR NEW.answerable IS NOT OLD.answerable OR NEW.answered_at_ms IS NOT OLD.answered_at_ms OR NEW.answered_by IS NOT OLD.answered_by OR NEW.answered_via IS NOT OLD.answered_via OR NEW.applied_at_ms IS NOT OLD.applied_at_ms OR NEW.apply_id IS NOT OLD.apply_id OR NEW.apply_status IS NOT OLD.apply_status OR NEW.created_at_ms IS NOT OLD.created_at_ms OR NEW.id IS NOT OLD.id OR NEW.origin IS NOT OLD.origin OR NEW.questions_json IS NOT OLD.questions_json OR NEW.ref IS NOT OLD.ref OR NEW.route_json IS NOT OLD.route_json OR NEW.routed_at_ms IS NOT OLD.routed_at_ms OR NEW.routed_to IS NOT OLD.routed_to OR NEW.session_id IS NOT OLD.session_id OR NEW.state IS NOT OLD.state OR NEW.title IS NOT OLD.title OR NEW.wait_kind IS NOT OLD.wait_kind)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_catalog_retired_version_delete
CREATE TRIGGER catalog_presentation_version_catalog_retired_version_delete AFTER DELETE ON catalog_retired_version
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_catalog_retired_version_insert
CREATE TRIGGER catalog_presentation_version_catalog_retired_version_insert AFTER INSERT ON catalog_retired_version
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_catalog_retired_version_update
CREATE TRIGGER catalog_presentation_version_catalog_retired_version_update AFTER UPDATE OF id,version ON catalog_retired_version WHEN (NEW.id IS NOT OLD.id OR NEW.version IS NOT OLD.version)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_catalog_sessions_delete
CREATE TRIGGER catalog_presentation_version_catalog_sessions_delete AFTER DELETE ON catalog_sessions
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_catalog_sessions_insert
CREATE TRIGGER catalog_presentation_version_catalog_sessions_insert AFTER INSERT ON catalog_sessions
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_catalog_sessions_update
CREATE TRIGGER catalog_presentation_version_catalog_sessions_update AFTER UPDATE OF session_id,unavailable_reason,waiting_on,background_tasks,usage_limit,turn_stop,host_queue_count,projection_generation,projection_fingerprint,misses,discovery_failures,retired,current,host_project ON catalog_sessions WHEN (NEW.session_id IS NOT OLD.session_id OR NEW.unavailable_reason IS NOT OLD.unavailable_reason OR NEW.waiting_on IS NOT OLD.waiting_on OR NEW.background_tasks IS NOT OLD.background_tasks OR NEW.usage_limit IS NOT OLD.usage_limit OR NEW.turn_stop IS NOT OLD.turn_stop OR NEW.host_queue_count IS NOT OLD.host_queue_count OR NEW.projection_generation IS NOT OLD.projection_generation OR NEW.projection_fingerprint IS NOT OLD.projection_fingerprint OR NEW.misses IS NOT OLD.misses OR NEW.discovery_failures IS NOT OLD.discovery_failures OR NEW.retired IS NOT OLD.retired OR NEW.current IS NOT OLD.current OR NEW.host_project IS NOT OLD.host_project)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinator_history_delete
CREATE TRIGGER catalog_presentation_version_coordinator_history_delete AFTER DELETE ON coordinator_history
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinator_history_insert
CREATE TRIGGER catalog_presentation_version_coordinator_history_insert AFTER INSERT ON coordinator_history
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinator_history_update
CREATE TRIGGER catalog_presentation_version_coordinator_history_update AFTER UPDATE OF acknowledged_at_ms,action,announced_at_ms,at_ms,changed_by,id,name,notify_sender,person_notified_at_ms,previous_name,previous_session_id,project_id,reason,session_id ON coordinator_history WHEN (NEW.acknowledged_at_ms IS NOT OLD.acknowledged_at_ms OR NEW.action IS NOT OLD.action OR NEW.announced_at_ms IS NOT OLD.announced_at_ms OR NEW.at_ms IS NOT OLD.at_ms OR NEW.changed_by IS NOT OLD.changed_by OR NEW.id IS NOT OLD.id OR NEW.name IS NOT OLD.name OR NEW.notify_sender IS NOT OLD.notify_sender OR NEW.person_notified_at_ms IS NOT OLD.person_notified_at_ms OR NEW.previous_name IS NOT OLD.previous_name OR NEW.previous_session_id IS NOT OLD.previous_session_id OR NEW.project_id IS NOT OLD.project_id OR NEW.reason IS NOT OLD.reason OR NEW.session_id IS NOT OLD.session_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinator_replaced_delete
CREATE TRIGGER catalog_presentation_version_coordinator_replaced_delete AFTER DELETE ON coordinator_replaced
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinator_replaced_insert
CREATE TRIGGER catalog_presentation_version_coordinator_replaced_insert AFTER INSERT ON coordinator_replaced
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinator_replaced_update
CREATE TRIGGER catalog_presentation_version_coordinator_replaced_update AFTER UPDATE OF project_id,session_id ON coordinator_replaced WHEN (NEW.project_id IS NOT OLD.project_id OR NEW.session_id IS NOT OLD.session_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinators_delete
CREATE TRIGGER catalog_presentation_version_coordinators_delete AFTER DELETE ON coordinators
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinators_insert
CREATE TRIGGER catalog_presentation_version_coordinators_insert AFTER INSERT ON coordinators
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_coordinators_update
CREATE TRIGGER catalog_presentation_version_coordinators_update AFTER UPDATE OF changed_at_ms,changed_by,cwd,host_id,instructions,model,moved_from,name,permission_mode,project_id,project_root,session_id,surface ON coordinators WHEN (NEW.changed_at_ms IS NOT OLD.changed_at_ms OR NEW.changed_by IS NOT OLD.changed_by OR NEW.cwd IS NOT OLD.cwd OR NEW.host_id IS NOT OLD.host_id OR NEW.instructions IS NOT OLD.instructions OR NEW.model IS NOT OLD.model OR NEW.moved_from IS NOT OLD.moved_from OR NEW.name IS NOT OLD.name OR NEW.permission_mode IS NOT OLD.permission_mode OR NEW.project_id IS NOT OLD.project_id OR NEW.project_root IS NOT OLD.project_root OR NEW.session_id IS NOT OLD.session_id OR NEW.surface IS NOT OLD.surface)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_limit_resumes_delete
CREATE TRIGGER catalog_presentation_version_limit_resumes_delete AFTER DELETE ON limit_resumes
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_limit_resumes_insert
CREATE TRIGGER catalog_presentation_version_limit_resumes_insert AFTER INSERT ON limit_resumes
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_limit_resumes_update
CREATE TRIGGER catalog_presentation_version_limit_resumes_update AFTER UPDATE OF account_key,attempts,refused_at_ms,resets_at_ms,session_id,state,turn_id,updated_at_ms ON limit_resumes WHEN (NEW.account_key IS NOT OLD.account_key OR NEW.attempts IS NOT OLD.attempts OR NEW.refused_at_ms IS NOT OLD.refused_at_ms OR NEW.resets_at_ms IS NOT OLD.resets_at_ms OR NEW.session_id IS NOT OLD.session_id OR NEW.state IS NOT OLD.state OR NEW.turn_id IS NOT OLD.turn_id OR NEW.updated_at_ms IS NOT OLD.updated_at_ms)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_message_queue_delete
CREATE TRIGGER catalog_presentation_version_message_queue_delete AFTER DELETE ON message_queue
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_message_queue_insert
CREATE TRIGGER catalog_presentation_version_message_queue_insert AFTER INSERT ON message_queue
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_message_queue_update
CREATE TRIGGER catalog_presentation_version_message_queue_update AFTER UPDATE OF attempts,author,available_at_ms,busy_delivery,dead_at_ms,deferrals,delivered,delivery_key,evidence,host_request,id,inflight_at_ms,last_error,message,model,operation,person_name,queued_at,refusal,refused,relay_hops,session_id,source_session_id,status,turn_options,updated_at,uploads ON message_queue WHEN (NEW.attempts IS NOT OLD.attempts OR NEW.author IS NOT OLD.author OR NEW.available_at_ms IS NOT OLD.available_at_ms OR NEW.busy_delivery IS NOT OLD.busy_delivery OR NEW.dead_at_ms IS NOT OLD.dead_at_ms OR NEW.deferrals IS NOT OLD.deferrals OR NEW.delivered IS NOT OLD.delivered OR NEW.delivery_key IS NOT OLD.delivery_key OR NEW.evidence IS NOT OLD.evidence OR NEW.host_request IS NOT OLD.host_request OR NEW.id IS NOT OLD.id OR NEW.inflight_at_ms IS NOT OLD.inflight_at_ms OR NEW.last_error IS NOT OLD.last_error OR NEW.message IS NOT OLD.message OR NEW.model IS NOT OLD.model OR NEW.operation IS NOT OLD.operation OR NEW.person_name IS NOT OLD.person_name OR NEW.queued_at IS NOT OLD.queued_at OR NEW.refusal IS NOT OLD.refusal OR NEW.refused IS NOT OLD.refused OR NEW.relay_hops IS NOT OLD.relay_hops OR NEW.session_id IS NOT OLD.session_id OR NEW.source_session_id IS NOT OLD.source_session_id OR NEW.status IS NOT OLD.status OR NEW.turn_options IS NOT OLD.turn_options OR NEW.updated_at IS NOT OLD.updated_at OR NEW.uploads IS NOT OLD.uploads)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_projects_delete
CREATE TRIGGER catalog_presentation_version_projects_delete AFTER DELETE ON projects
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_projects_insert
CREATE TRIGGER catalog_presentation_version_projects_insert AFTER INSERT ON projects
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_projects_update
CREATE TRIGGER catalog_presentation_version_projects_update AFTER UPDATE OF added,archived_at_ms,created_at_ms,id,project_key,repository,title,workspace_root ON projects WHEN (NEW.added IS NOT OLD.added OR NEW.archived_at_ms IS NOT OLD.archived_at_ms OR NEW.created_at_ms IS NOT OLD.created_at_ms OR NEW.id IS NOT OLD.id OR NEW.project_key IS NOT OLD.project_key OR NEW.repository IS NOT OLD.repository OR NEW.title IS NOT OLD.title OR NEW.workspace_root IS NOT OLD.workspace_root)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_archive_delete
CREATE TRIGGER catalog_presentation_version_session_archive_delete AFTER DELETE ON session_archive
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_archive_insert
CREATE TRIGGER catalog_presentation_version_session_archive_insert AFTER INSERT ON session_archive
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_archive_update
CREATE TRIGGER catalog_presentation_version_session_archive_update AFTER UPDATE OF archived_at_ms,session_id ON session_archive WHEN (NEW.archived_at_ms IS NOT OLD.archived_at_ms OR NEW.session_id IS NOT OLD.session_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_keep_alive_delete
CREATE TRIGGER catalog_presentation_version_session_keep_alive_delete AFTER DELETE ON session_keep_alive
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_keep_alive_insert
CREATE TRIGGER catalog_presentation_version_session_keep_alive_insert AFTER INSERT ON session_keep_alive
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_keep_alive_update
CREATE TRIGGER catalog_presentation_version_session_keep_alive_update AFTER UPDATE OF session_id,failed_at_ms,error ON session_keep_alive WHEN (NEW.session_id IS NOT OLD.session_id OR NEW.failed_at_ms IS NOT OLD.failed_at_ms OR NEW.error IS NOT OLD.error)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_moves_delete
CREATE TRIGGER catalog_presentation_version_session_moves_delete AFTER DELETE ON session_moves WHEN OLD.role='out'
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_moves_insert
CREATE TRIGGER catalog_presentation_version_session_moves_insert AFTER INSERT ON session_moves WHEN NEW.role='out'
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_moves_update
CREATE TRIGGER catalog_presentation_version_session_moves_update AFTER UPDATE OF conversation,peer_host,state,detail,outcome_unknown_at ON session_moves WHEN (NEW.role='out' OR OLD.role='out') AND (NEW.conversation IS NOT OLD.conversation OR NEW.peer_host IS NOT OLD.peer_host OR NEW.state IS NOT OLD.state OR NEW.detail IS NOT OLD.detail OR NEW.outcome_unknown_at IS NOT OLD.outcome_unknown_at)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_origins_delete
CREATE TRIGGER catalog_presentation_version_session_origins_delete AFTER DELETE ON session_origins
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_origins_insert
CREATE TRIGGER catalog_presentation_version_session_origins_insert AFTER INSERT ON session_origins
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_origins_update
CREATE TRIGGER catalog_presentation_version_session_origins_update AFTER UPDATE OF created_at,creator_session_id,session_id,via ON session_origins WHEN (NEW.created_at IS NOT OLD.created_at OR NEW.creator_session_id IS NOT OLD.creator_session_id OR NEW.session_id IS NOT OLD.session_id OR NEW.via IS NOT OLD.via)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_prs_delete
CREATE TRIGGER catalog_presentation_version_session_prs_delete AFTER DELETE ON session_prs WHEN OLD.unlinked=0
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_prs_insert
CREATE TRIGGER catalog_presentation_version_session_prs_insert AFTER INSERT ON session_prs WHEN NEW.unlinked=0
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_prs_update
CREATE TRIGGER catalog_presentation_version_session_prs_update AFTER UPDATE OF session_id,repo,number,url,source,unlinked,watch,title,state,head_sha,mergeable,review,checks,read_failures,linked_at_ms ON session_prs WHEN (NEW.unlinked=0 OR OLD.unlinked=0) AND (NEW.session_id IS NOT OLD.session_id OR NEW.repo IS NOT OLD.repo OR NEW.number IS NOT OLD.number OR NEW.url IS NOT OLD.url OR NEW.source IS NOT OLD.source OR NEW.unlinked IS NOT OLD.unlinked OR NEW.watch IS NOT OLD.watch OR NEW.title IS NOT OLD.title OR NEW.state IS NOT OLD.state OR NEW.head_sha IS NOT OLD.head_sha OR NEW.mergeable IS NOT OLD.mergeable OR NEW.review IS NOT OLD.review OR NEW.checks IS NOT OLD.checks OR (NEW.read_failures>=3) IS NOT (OLD.read_failures>=3) OR NEW.linked_at_ms IS NOT OLD.linked_at_ms)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_runtime_delete
CREATE TRIGGER catalog_presentation_version_session_runtime_delete AFTER DELETE ON session_runtime
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_runtime_insert
CREATE TRIGGER catalog_presentation_version_session_runtime_insert AFTER INSERT ON session_runtime
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_session_runtime_update
CREATE TRIGGER catalog_presentation_version_session_runtime_update AFTER UPDATE OF session_id,completed_turn_id ON session_runtime WHEN (NEW.session_id IS NOT OLD.session_id OR NEW.completed_turn_id IS NOT OLD.completed_turn_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_sessions_delete
CREATE TRIGGER catalog_presentation_version_sessions_delete AFTER DELETE ON sessions
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_sessions_insert
CREATE TRIGGER catalog_presentation_version_sessions_insert AFTER INSERT ON sessions
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_sessions_update
CREATE TRIGGER catalog_presentation_version_sessions_update AFTER UPDATE OF id,name,surface,status,ended,cwd,last_active_ms,runtime_kind,source,transport,host_id,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,registered_at,pinned_at_ms,snoozed_at_ms,snoozed_until_ms,snooze_wake ON sessions WHEN (NEW.id IS NOT OLD.id OR NEW.name IS NOT OLD.name OR NEW.surface IS NOT OLD.surface OR NEW.status IS NOT OLD.status OR NEW.ended IS NOT OLD.ended OR NEW.cwd IS NOT OLD.cwd OR NEW.last_active_ms IS NOT OLD.last_active_ms OR NEW.runtime_kind IS NOT OLD.runtime_kind OR NEW.source IS NOT OLD.source OR NEW.transport IS NOT OLD.transport OR NEW.host_id IS NOT OLD.host_id OR NEW.parent_session_id IS NOT OLD.parent_session_id OR NEW.root_session_id IS NOT OLD.root_session_id OR NEW.subagent_depth IS NOT OLD.subagent_depth OR NEW.agent_nickname IS NOT OLD.agent_nickname OR NEW.agent_role IS NOT OLD.agent_role OR NEW.registered_at IS NOT OLD.registered_at OR NEW.pinned_at_ms IS NOT OLD.pinned_at_ms OR NEW.snoozed_at_ms IS NOT OLD.snoozed_at_ms OR NEW.snoozed_until_ms IS NOT OLD.snoozed_until_ms OR NEW.snooze_wake IS NOT OLD.snooze_wake)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_tasks_delete
CREATE TRIGGER catalog_presentation_version_tasks_delete AFTER DELETE ON tasks
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_tasks_insert
CREATE TRIGGER catalog_presentation_version_tasks_insert AFTER INSERT ON tasks
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_tasks_update
CREATE TRIGGER catalog_presentation_version_tasks_update AFTER UPDATE OF child_session_id ON tasks WHEN (NEW.child_session_id IS NOT OLD.child_session_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_turn_ends_delete
CREATE TRIGGER catalog_presentation_version_turn_ends_delete AFTER DELETE ON turn_ends
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_turn_ends_insert
CREATE TRIGGER catalog_presentation_version_turn_ends_insert AFTER INSERT ON turn_ends
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_turn_ends_update
CREATE TRIGGER catalog_presentation_version_turn_ends_update AFTER UPDATE OF completed_turn_id,ended_at_ms,headline,next_json,session_id,source,started_at_ms,turn_id ON turn_ends WHEN (NEW.completed_turn_id IS NOT OLD.completed_turn_id OR NEW.ended_at_ms IS NOT OLD.ended_at_ms OR NEW.headline IS NOT OLD.headline OR NEW.next_json IS NOT OLD.next_json OR NEW.session_id IS NOT OLD.session_id OR NEW.source IS NOT OLD.source OR NEW.started_at_ms IS NOT OLD.started_at_ms OR NEW.turn_id IS NOT OLD.turn_id)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_work_records_delete
CREATE TRIGGER catalog_presentation_version_work_records_delete AFTER DELETE ON work_records WHEN OLD.session_id<>''
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_work_records_insert
CREATE TRIGGER catalog_presentation_version_work_records_insert AFTER INSERT ON work_records WHEN NEW.session_id<>''
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_presentation_version_work_records_update
CREATE TRIGGER catalog_presentation_version_work_records_update AFTER UPDATE OF session_id,worktree_path,branch,base_ref,state,settled_reason,cleanup,cleanup_reason,kept_paths ON work_records WHEN (NEW.session_id<>'' OR OLD.session_id<>'') AND (NEW.session_id IS NOT OLD.session_id OR NEW.worktree_path IS NOT OLD.worktree_path OR NEW.branch IS NOT OLD.branch OR NEW.base_ref IS NOT OLD.base_ref OR NEW.state IS NOT OLD.state OR NEW.settled_reason IS NOT OLD.settled_reason OR NEW.cleanup IS NOT OLD.cleanup OR NEW.cleanup_reason IS NOT OLD.cleanup_reason OR NEW.kept_paths IS NOT OLD.kept_paths)
BEGIN
	UPDATE catalog_presentation_version SET version=version+1;
END;

-- trigger catalog_retired_version_archived
CREATE TRIGGER catalog_retired_version_archived AFTER INSERT ON session_archive
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_project
CREATE TRIGGER catalog_retired_version_project AFTER UPDATE OF id,archived_at_ms ON projects
WHEN NEW.id IS NOT OLD.id OR NEW.archived_at_ms IS NOT OLD.archived_at_ms
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_project_added
CREATE TRIGGER catalog_retired_version_project_added AFTER INSERT ON projects
WHEN NEW.archived_at_ms>0
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_project_removed
CREATE TRIGGER catalog_retired_version_project_removed AFTER DELETE ON projects
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_rearchived
CREATE TRIGGER catalog_retired_version_rearchived AFTER UPDATE OF session_id ON session_archive
WHEN NEW.session_id IS NOT OLD.session_id
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_removed
CREATE TRIGGER catalog_retired_version_removed AFTER DELETE ON catalog_sessions
WHEN OLD.retired=1
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_row
CREATE TRIGGER catalog_retired_version_row AFTER UPDATE OF session_id,retired,host_project,unavailable_reason ON catalog_sessions
WHEN (OLD.retired=1 OR NEW.retired=1) AND (NEW.session_id IS NOT OLD.session_id OR NEW.retired IS NOT OLD.retired OR NEW.host_project IS NOT OLD.host_project OR NEW.unavailable_reason IS NOT OLD.unavailable_reason)
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_row_added
CREATE TRIGGER catalog_retired_version_row_added AFTER INSERT ON catalog_sessions
WHEN NEW.retired=1
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_session
CREATE TRIGGER catalog_retired_version_session AFTER UPDATE OF id,status,surface,host_id ON sessions
WHEN (NEW.id IS NOT OLD.id OR NEW.status IS NOT OLD.status OR NEW.surface IS NOT OLD.surface OR NEW.host_id IS NOT OLD.host_id)
	AND EXISTS (SELECT 1 FROM catalog_sessions WHERE session_id IN (OLD.id,NEW.id) AND retired=1)
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

-- trigger catalog_retired_version_unarchived
CREATE TRIGGER catalog_retired_version_unarchived AFTER DELETE ON session_archive
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;

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

-- trigger catalog_sessions_retired_current
CREATE TRIGGER catalog_sessions_retired_current AFTER UPDATE OF current ON catalog_sessions
WHEN NEW.current=1 AND NEW.retired=1
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE session_id=NEW.session_id;
END;

-- trigger catalog_sessions_unretire_family
CREATE TRIGGER catalog_sessions_unretire_family AFTER UPDATE OF retired ON catalog_sessions
WHEN OLD.retired=1 AND NEW.retired=0
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE retired=1 AND session_id IN (SELECT id FROM sessions WHERE parent_session_id<>'' AND parent_session_id=NEW.session_id
		UNION SELECT id FROM sessions WHERE root_session_id<>'' AND root_session_id IN (NEW.session_id,(SELECT NULLIF(root_session_id,'') FROM sessions WHERE id=NEW.session_id)));
END;

-- trigger catalog_sessions_unretire_family_insert
CREATE TRIGGER catalog_sessions_unretire_family_insert AFTER INSERT ON catalog_sessions
WHEN NEW.retired=0
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE retired=1 AND session_id IN (SELECT id FROM sessions WHERE parent_session_id<>'' AND parent_session_id=NEW.session_id
		UNION SELECT id FROM sessions WHERE root_session_id<>'' AND root_session_id IN (NEW.session_id,(SELECT NULLIF(root_session_id,'') FROM sessions WHERE id=NEW.session_id)));
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

-- trigger message_queue_host_count_delete
CREATE TRIGGER message_queue_host_count_delete AFTER DELETE ON message_queue
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE id=OLD.session_id AND host_id<>'';
END;

-- trigger message_queue_host_count_insert
CREATE TRIGGER message_queue_host_count_insert AFTER INSERT ON message_queue
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE id=NEW.session_id AND host_id<>'';
END;

-- trigger message_queue_host_count_update
CREATE TRIGGER message_queue_host_count_update AFTER UPDATE OF status,session_id ON message_queue
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) SELECT id FROM sessions WHERE id IN (OLD.session_id,NEW.session_id) AND host_id<>'';
END;

-- trigger sessions_host_count_insert
CREATE TRIGGER sessions_host_count_insert AFTER INSERT ON sessions
WHEN NEW.host_id<>''
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) VALUES(NEW.id);
END;

-- trigger sessions_host_count_mirrored
CREATE TRIGGER sessions_host_count_mirrored AFTER UPDATE OF host_id ON sessions
WHEN NEW.host_id<>''
BEGIN
	INSERT OR IGNORE INTO host_queue_dirty(session_id) VALUES(NEW.id);
END;

-- trigger sessions_unretire
CREATE TRIGGER sessions_unretire AFTER UPDATE OF ended,parent_session_id ON sessions
WHEN (OLD.ended=1 AND NEW.ended=0) OR NEW.parent_session_id IS NOT OLD.parent_session_id
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE session_id=NEW.id AND retired=1;
END;

