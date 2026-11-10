package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func unseenRow(id string, age time.Duration) surface.Session {
	row := claudeRow(id)
	row.Status, row.Ended, row.LastActive = surface.StatusOffline, true, time.Now().Add(-age)
	return row
}

func TestAConversationAgenthailNeverSawIsRecordedOnceAndListedAsHistory(t *testing.T) {
	d, r, adapter := endedFixture(t)
	named := claudeRow("known")
	named.Name = "kept name"
	if err := r.RegisterSession(named); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveSession(named.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	live := claudeRow("live")
	live.Status = surface.StatusBusy
	adapter.sessions[live.ID] = live
	old := unseenRow("from-a-terminal", 90*24*time.Hour)
	old.Name = "fix the parser"
	renamed := unseenRow("known", time.Hour)
	renamed.Name = "transcript name"
	adapter.unseen = []surface.Session{old, renamed, unseenRow("live", time.Hour)}
	adapter.changed = old.LastActive

	d.discoverCatalog(context.Background())
	d.discoverCatalog(context.Background())

	stored, err := r.Session(old.ID)
	if err != nil || stored.Name != "fix the parser" || stored.Status != surface.StatusOffline || stored.Transcript != old.Transcript {
		t.Fatalf("the unseen conversation is %+v (%v), want an offline row with its transcript", stored, err)
	}
	if !viewHas(t, d, "all", old.ID) || viewHas(t, d, "recent", old.ID) {
		t.Fatal("a 90-day-old conversation must be listed in All as history, not in Recent")
	}
	if kept, err := r.Session(named.ID); err != nil || kept.Name != "kept name" {
		t.Fatalf("a known row was rewritten from its transcript: %+v %v", kept, err)
	}
	if archive, err := r.ArchiveState(); err != nil || archive.Sessions[named.ID].IsZero() {
		t.Fatalf("an archived row came back unarchived: %v", err)
	}
	if got, err := r.Session(live.ID); err != nil || got.Status == surface.StatusOffline {
		t.Fatalf("a live session was recorded as ended: %+v %v", got, err)
	}
	asked := len(adapter.asked)
	d.discoverCatalog(context.Background())
	if len(adapter.asked) != asked {
		t.Fatal("unseen conversations were looked for between hourly checks")
	}
}
