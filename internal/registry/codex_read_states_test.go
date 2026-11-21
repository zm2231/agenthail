package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAListingThatPredatesAMarkDoesNotUndoIt(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.db.Exec(`INSERT INTO sessions(id,surface,name) VALUES('t','codex','t')`); err != nil {
		t.Fatal(err)
	}
	listedAt := time.Now()
	read := surface.ReadState{Revision: "rev-2"}
	if err := r.RecordCodexReadStates(map[string]surface.ReadState{"t": read}, listedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordListedCodexReadStates(map[string]surface.ReadState{"t": {FirstUnread: "turn-1", Revision: "rev-1"}}, listedAt); err != nil {
		t.Fatal(err)
	}
	if stored, _, err := r.CodexReadState("t"); err != nil || stored != read {
		t.Fatalf("stored receipt = %+v err=%v, want the mark %+v", stored, err, read)
	}
	later := surface.ReadState{FirstUnread: "turn-2", Revision: "rev-3"}
	if err := r.RecordListedCodexReadStates(map[string]surface.ReadState{"t": later}, listedAt.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if stored, _, err := r.CodexReadState("t"); err != nil || stored != later {
		t.Fatalf("stored receipt = %+v err=%v, want the later listing %+v", stored, err, later)
	}
}
