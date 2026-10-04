-- Registry database written by internal/registry at 1d64825 (fix: preserve source errors and spoken content boundaries).
-- Regenerate with generate.sh in this directory.
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE sessions (
	id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '',
	has_local INTEGER NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT '', transport TEXT NOT NULL DEFAULT '',
	configured_model TEXT NOT NULL DEFAULT '',
	last_active_ms INTEGER NOT NULL DEFAULT 0,
	registered_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO sessions VALUES('sender','codex','Sender','',0,'','',0,'','','',0,'2026-10-04 23:05:23','2026-10-04 23:05:23');
INSERT INTO sessions VALUES('target','codex','Target','/work/target',0,'','',0,'','','',0,'2026-10-04 23:05:23','2026-10-04 23:05:23');
CREATE TABLE aliases (
	name TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO aliases VALUES('builder','target','2026-10-04 23:05:23');
CREATE TABLE channels (
	id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE channel_members (
	channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	PRIMARY KEY (channel_id, session_id)
);
CREATE TABLE routes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	from_session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	to_session TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	channel_id TEXT REFERENCES channels(id) ON DELETE SET NULL,
	pattern TEXT NOT NULL DEFAULT '.*',
	once_only INTEGER NOT NULL DEFAULT 0,
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO routes VALUES(1,'sender','target',NULL,'.*',0,1,'2026-10-04 23:05:23');
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
	expires_at_ms INTEGER NOT NULL DEFAULT 0,
	evidence TEXT NOT NULL DEFAULT '',
	operation TEXT NOT NULL DEFAULT 'message',
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, busy_delivery TEXT NOT NULL DEFAULT '');
INSERT INTO message_queue VALUES(1,'target','delivered hello','2026-09-01 00:00:00',1,'delivered',1,'',0,0,'','',0,'','{}',1788224400000,'delivered','message','2026-10-04 23:05:23','');
INSERT INTO message_queue VALUES(2,'target','pending hello','2026-09-01 00:00:00',0,'pending',0,'',0,0,'','',0,'sender','{}',1788224400000,'','message','2026-10-04 23:05:23','');
CREATE TABLE session_runtime (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	last_status TEXT NOT NULL DEFAULT 'unknown',
	active_turn_id TEXT NOT NULL DEFAULT '',
	completed_turn_id TEXT NOT NULL DEFAULT '',
	relay_hops INTEGER NOT NULL DEFAULT 0,
	notification_armed INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, runtime_launcher TEXT NOT NULL DEFAULT '', runtime_location BLOB NOT NULL DEFAULT '{}', runtime_focusable INTEGER NOT NULL DEFAULT 0);
CREATE TABLE launcher_pending (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	launcher TEXT NOT NULL,
	agent TEXT NOT NULL,
	cwd TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	alias TEXT NOT NULL DEFAULT '',
	location BLOB NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE relay_deliveries (
	route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
	completion_id TEXT NOT NULL,
	delivered_at TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (route_id, completion_id)
);
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
	error TEXT NOT NULL DEFAULT ''
);
INSERT INTO delivery_history VALUES(1,'2026-10-04 23:05:23','queued','target','',0,1,'','delivered hello','','');
INSERT INTO delivery_history VALUES(2,'2026-10-04 23:05:23','queued','target','',0,2,'','pending hello','','');
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
CREATE TABLE device_pairings (
	id TEXT PRIMARY KEY,
	secret_hash TEXT NOT NULL UNIQUE,
	requested_name TEXT NOT NULL DEFAULT '',
	scopes TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE paired_devices (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	scopes TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	last_seen_at TEXT NOT NULL DEFAULT '',
	revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE device_push_targets (
	device_id TEXT PRIMARY KEY REFERENCES paired_devices(id) ON DELETE CASCADE,
	installation_id TEXT NOT NULL,
	credential TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE daemon_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	entity_id TEXT NOT NULL DEFAULT '',
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE session_journal (
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	kind TEXT NOT NULL,
	provider_key TEXT NOT NULL DEFAULT '',
	payload BLOB NOT NULL,
	observed_at TEXT NOT NULL,
	bytes INTEGER NOT NULL,
	body_ref TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, seq)
);
INSERT INTO session_journal VALUES('target',1,'text','timeline:old',X'7b226974656d4964223a226f6c64227d','2026-10-04T23:05:23.993729Z',16,'');
CREATE TABLE session_journal_state (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	next_seq INTEGER NOT NULL DEFAULT 0,
	retained_bytes INTEGER NOT NULL DEFAULT 0,
	source_epoch TEXT NOT NULL DEFAULT '',
	pruned_before INTEGER NOT NULL DEFAULT 0,
	seed_status TEXT NOT NULL DEFAULT 'unknown',
	seed_seq INTEGER NOT NULL DEFAULT 0
);
INSERT INTO session_journal_state VALUES('target',1,16,'',0,'succeeded',1);
CREATE TABLE session_journal_bodies (
	ref TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	body BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE action_receipts (
	idempotency_key TEXT PRIMARY KEY,
	request_hash TEXT NOT NULL,
	status TEXT NOT NULL,
	receipt BLOB NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE delivery_intents (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sender_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	target_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	provider_key TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	queue_id INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	evidence TEXT NOT NULL,
	failure TEXT NOT NULL DEFAULT '',
	dismissed_at TEXT,
	notification_queue_id INTEGER REFERENCES message_queue(id) ON DELETE SET NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE catalog_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	dedupe_key TEXT NOT NULL UNIQUE,
	type TEXT NOT NULL,
	entity_id TEXT NOT NULL,
	payload BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE catalog_state (
	id INTEGER PRIMARY KEY CHECK (id=1),
	host_epoch TEXT NOT NULL
);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('routes',1);
INSERT INTO sqlite_sequence VALUES('message_queue',2);
INSERT INTO sqlite_sequence VALUES('delivery_history',2);
INSERT INTO sqlite_sequence VALUES('attention_items',0);
CREATE INDEX delivery_history_created_at ON delivery_history(created_at DESC, id DESC);
CREATE INDEX attention_items_open ON attention_items(resolved_at, created_at DESC, id DESC);
CREATE INDEX device_pairings_expires ON device_pairings(expires_at);
CREATE INDEX paired_devices_active ON paired_devices(revoked_at, created_at DESC);
CREATE INDEX daemon_events_created ON daemon_events(created_at DESC, id DESC);
CREATE UNIQUE INDEX session_journal_provider_key
	ON session_journal(session_id, provider_key) WHERE provider_key!='';
CREATE INDEX session_journal_bodies_session ON session_journal_bodies(session_id);
CREATE UNIQUE INDEX delivery_intents_provider_key
	ON delivery_intents(target_session_id, provider_key) WHERE provider_key!='';
CREATE INDEX delivery_intents_sender_status
	ON delivery_intents(sender_session_id, status, id DESC);
CREATE INDEX catalog_events_created ON catalog_events(created_at DESC, seq DESC);
CREATE UNIQUE INDEX message_queue_delivery_key
		ON message_queue(delivery_key) WHERE delivery_key!='';
CREATE UNIQUE INDEX aliases_session_id ON aliases(session_id);
COMMIT;
PRAGMA user_version=11;
