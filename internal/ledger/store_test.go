package ledger_test

import (
	"database/sql"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r.WriteDB()
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

	event, err := s.RecordUtterance(ledger.Utterance{ProjectKey: "proj-1", Ref: "msg-1", SpeakerID: "sess-1", TargetID: "target-1", Surface: "claude", Text: "build me a widget"})
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

	if _, err := s.RecordUtterance(ledger.Utterance{ProjectKey: "proj-1", Ref: "m1", SpeakerID: "s1", Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUtterance(ledger.Utterance{ProjectKey: "proj-2", Ref: "m2", SpeakerID: "s2", Text: "second"}); err != nil {
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

	if _, err := s.RecordUtterance(ledger.Utterance{ProjectKey: "proj-1", Ref: "m1", SpeakerID: "s1", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUtterance(ledger.Utterance{ProjectKey: "proj-1", Ref: "m2", SpeakerID: "s1", Text: "world"}); err != nil {
		t.Fatal(err)
	}

	seq, err = s.LatestSeq("proj-1")
	if err != nil || seq != 2 {
		t.Fatalf("expected 2, got %d err=%v", seq, err)
	}
}
