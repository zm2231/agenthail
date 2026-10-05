-- Registry database written by internal/registry at 86dce76 (fix: status column shows real state instead of binary dot).
-- Regenerate with generate.sh in this directory.
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE sessions (
	id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '',
	has_local INTEGER NOT NULL DEFAULT 0,
	registered_at TEXT NOT NULL DEFAULT (datetime('now')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO sessions VALUES('sender','codex','Sender','',0,'','',0,'2026-10-04 23:05:17','2026-10-04 23:05:17');
INSERT INTO sessions VALUES('target','codex','Target','/work/target',0,'','',0,'2026-10-04 23:05:17','2026-10-04 23:05:17');
CREATE TABLE aliases (
	name TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO aliases VALUES('builder','target','2026-10-04 23:05:17');
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
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO routes VALUES(1,'sender','target',NULL,'.*','2026-10-04 23:05:17');
CREATE TABLE message_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	message TEXT NOT NULL, queued_at TEXT NOT NULL DEFAULT (datetime('now')),
	delivered INTEGER NOT NULL DEFAULT 0
);
INSERT INTO message_queue VALUES(1,'target','delivered hello','2026-09-01 00:00:00',1);
INSERT INTO message_queue VALUES(2,'target','pending hello','2026-09-01 00:00:00',0);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('routes',1);
INSERT INTO sqlite_sequence VALUES('message_queue',2);
COMMIT;
PRAGMA user_version=0;
