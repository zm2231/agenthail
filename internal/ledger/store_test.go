package ledger_test

import (
	"database/sql"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE ledger_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			project_key TEXT NOT NULL,
			type TEXT NOT NULL,
			payload BLOB NOT NULL,
			actor_session_id TEXT NOT NULL DEFAULT '',
			actor_host_id TEXT NOT NULL DEFAULT '',
			owner_seq INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX ledger_events_project ON ledger_events(project_key, seq)`,
		`CREATE TABLE ledger_state (
			project_key TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			owner_host_id TEXT NOT NULL DEFAULT '',
			owner_cursor INTEGER NOT NULL DEFAULT 0,
			deadline_ms INTEGER NOT NULL DEFAULT 0,
			freeze_version TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestEnableDisable(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)

	enabled, err := s.Enabled("proj-1")
	if err != nil || enabled {
		t.Fatalf("expected disabled, got enabled=%v err=%v", enabled, err)
	}

	if err := s.Enable("proj-1"); err != nil {
		t.Fatal(err)
	}
	enabled, err = s.Enabled("proj-1")
	if err != nil || !enabled {
		t.Fatalf("expected enabled, got enabled=%v err=%v", enabled, err)
	}

	if err := s.Disable("proj-1"); err != nil {
		t.Fatal(err)
	}
	enabled, err = s.Enabled("proj-1")
	if err != nil || enabled {
		t.Fatalf("expected disabled after disable, got enabled=%v err=%v", enabled, err)
	}
}

func TestRecordUtterance(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)

	event, err := s.RecordUtterance("proj-1", "msg-1", "sess-1", "claude", "build me a widget")
	if err != nil {
		t.Fatal(err)
	}
	if event.Seq < 1 {
		t.Fatalf("expected seq >= 1, got %d", event.Seq)
	}
	if event.Type != ledger.EventUtterance {
		t.Fatalf("expected utterance type, got %s", event.Type)
	}

	events, err := s.Events("proj-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Seq != event.Seq {
		t.Fatalf("seq mismatch: %d != %d", events[0].Seq, event.Seq)
	}
}

func TestEventsFilterByProject(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)

	if _, err := s.RecordUtterance("proj-1", "m1", "s1", "claude", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUtterance("proj-2", "m2", "s2", "claude", "second"); err != nil {
		t.Fatal(err)
	}

	events, err := s.Events("proj-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for proj-1, got %d", len(events))
	}
}

func TestLatestSeq(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)

	seq, err := s.LatestSeq("proj-1")
	if err != nil || seq != 0 {
		t.Fatalf("expected 0 for empty, got %d err=%v", seq, err)
	}

	if _, err := s.RecordUtterance("proj-1", "m1", "s1", "claude", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUtterance("proj-1", "m2", "s1", "claude", "world"); err != nil {
		t.Fatal(err)
	}

	seq, err = s.LatestSeq("proj-1")
	if err != nil || seq != 2 {
		t.Fatalf("expected 2, got %d err=%v", seq, err)
	}
}
