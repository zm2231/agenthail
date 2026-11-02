package ledger_test

import (
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestPersonCaptureRecordsUtterance(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)
	if err := s.Enable("proj-1"); err != nil {
		t.Fatal(err)
	}
	capture := ledger.PersonCapture(s)
	if err := capture(ledger.Utterance{
		ProjectKey: "proj-1",
		CaptureRef: "msg-1",
		SpeakerID:  "sess-1",
		TargetID:   "target-1",
		Surface:    "claude",
		Text:       "build me a widget",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events("proj-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 utterance event, got %d", len(events))
	}
	if events[0].Type != ledger.EventUtterance {
		t.Fatalf("expected utterance type, got %s", events[0].Type)
	}
}

func TestPersonCaptureSkipsDisabledProject(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)
	capture := ledger.PersonCapture(s)
	if err := capture(ledger.Utterance{
		ProjectKey: "proj-1",
		CaptureRef: "msg-1",
		SpeakerID:  "sess-1",
		Surface:    "claude",
		Text:       "build me a widget",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events("proj-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for disabled project, got %d", len(events))
	}
}

func TestPersonCaptureSkipsEmptyText(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)
	if err := s.Enable("proj-1"); err != nil {
		t.Fatal(err)
	}
	capture := ledger.PersonCapture(s)
	if err := capture(ledger.Utterance{
		ProjectKey: "proj-1",
		CaptureRef: "msg-1",
		SpeakerID:  "sess-1",
		Surface:    "claude",
		Text:       "",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events("proj-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for empty text, got %d", len(events))
	}
}
