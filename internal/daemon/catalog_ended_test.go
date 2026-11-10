package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type endedSurface struct {
	*daemonSurface
	missing map[string]bool
	changed time.Time
	checks  atomic.Int32
	unseen      []surface.Session
	asked       []string
	entrypoints map[string]string
}

func (s *endedSurface) ConversationEntrypoint(transcript string) string {
	return s.entrypoints[transcript]
}

func (s *endedSurface) UnseenConversations(known func(string) bool) []surface.Session {
	var out []surface.Session
	for _, session := range s.unseen {
		s.asked = append(s.asked, session.ID)
		if !known(session.ID) {
			out = append(out, session)
		}
	}
	return out
}

func (s *endedSurface) EndedConversation(transcript string) (bool, time.Time) {
	s.checks.Add(1)
	if transcript == "" || s.missing[transcript] {
		return false, time.Time{}
	}
	return true, s.changed
}

func endedFixture(t *testing.T) (*Daemon, *registry.Registry, *endedSurface) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, base, _, _ := daemonFixture(t)
	base.kind = surface.KindClaude
	base.sessions = map[string]surface.Session{}
	adapter := &endedSurface{daemonSurface: base, missing: map[string]bool{}, changed: time.Now()}
	d.Surfaces = []surface.Surface{adapter}
	return d, r, adapter
}

func claudeRow(id string) surface.Session {
	return surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: "/fixture/" + id + ".jsonl", HasLocal: true, Cwd: "/fixture", LastActive: time.Now().Add(-72 * time.Hour)}
}

func catalogHasRow(t *testing.T, r *registry.Registry, id string) bool {
	t.Helper()
	_, found, err := r.CatalogProjectionFingerprint(id)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestAnEndedClaudeSessionStaysInHistoryAndItsTranscriptIsCheckedAtMostHourly(t *testing.T) {
	d, r, adapter := endedFixture(t)
	session := claudeRow("worker")
	adapter.sessions[session.ID] = session
	d.discoverCatalog(context.Background())
	delete(adapter.sessions, session.ID)
	for range 4 {
		d.discoverCatalog(context.Background())
	}
	if !catalogHasRow(t, r, session.ID) {
		t.Fatal("an ended session with a transcript left the catalog")
	}
	stored, err := r.Session(session.ID)
	if err != nil || stored.Status != surface.StatusOffline {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	checks := adapter.checks.Load()
	d.discoverCatalog(context.Background())
	if adapter.checks.Load() != checks {
		t.Fatalf("a pass re-checked kept transcripts: %d->%d", checks, adapter.checks.Load())
	}
	adapter.missing[session.Transcript] = true
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, session.ID) {
		t.Fatal("a kept row was removed before its hourly check")
	}
	d.catalogEnded.rechecked[surface.KindClaude] = time.Now().Add(-catalogEndedRecheck)
	d.discoverCatalog(context.Background())
	if catalogHasRow(t, r, session.ID) {
		t.Fatal("the hourly check kept a conversation whose transcript is gone")
	}
}

func TestALeadThatLostItsRowGetsItBackAsCurrent(t *testing.T) {
	d, r, _ := endedFixture(t)
	session := claudeRow("lead-session")
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project", ProjectRoot: "/fixture", Name: "old-lead", Surface: string(surface.KindClaude), Cwd: "/fixture"}, registry.CoordinatorChange{Replace: true, SessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, session.ID) {
		t.Fatal("a resumable lead has no catalog row")
	}
	view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "current", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Sessions) != 1 || view.Sessions[0].ID != session.ID || !view.Sessions[0].Current || view.Sessions[0].CurrentReason != "lead" || view.Sessions[0].Status != surface.StatusOffline {
		t.Fatalf("view=%+v", view.Sessions)
	}
}

func TestASessionThatLostItsRowComesBackWhileItsTranscriptIsThereHoweverOld(t *testing.T) {
	d, r, adapter := endedFixture(t)
	for _, id := range []string{"gone", "stale-status"} {
		session := claudeRow(id)
		if id == "stale-status" {
			session.Status = surface.StatusBusy
		}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	adapter.missing["/fixture/gone.jsonl"] = true
	d.discoverCatalog(context.Background())
	if catalogHasRow(t, r, "gone") {
		t.Fatal("a session with no transcript came back")
	}
	if !catalogHasRow(t, r, "stale-status") {
		t.Fatal("a recent resumable session did not come back")
	}
	stored, err := r.Session("stale-status")
	if err != nil || stored.Status != surface.StatusOffline {
		t.Fatalf("a restored session must be offline: %+v err=%v", stored, err)
	}
	old := claudeRow("old")
	old.LastActive = time.Now().Add(-40 * 24 * time.Hour)
	if err := r.RegisterSession(old); err != nil {
		t.Fatal(err)
	}
	adapter.changed = old.LastActive
	d.catalogEnded.rechecked[surface.KindClaude] = time.Now().Add(-catalogEndedRecheck)
	d.discoverCatalog(context.Background())
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, "old") {
		t.Fatal("a session whose transcript last changed 40 days ago did not come back")
	}
	if !viewHas(t, d, "all", "old") || viewHas(t, d, "recent", "old") {
		t.Fatal("a 40-day-old restored session must be history: listed in All, not in Recent")
	}
}

func viewHas(t *testing.T, d *Daemon, scope, id string) bool {
	t.Helper()
	view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: scope, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Sessions {
		if row.ID == id {
			return true
		}
	}
	return false
}

func TestRestoringSessionsIsBoundedPerPass(t *testing.T) {
	d, r, _ := endedFixture(t)
	for index := range catalogRestoreBatch + 3 {
		if err := r.RegisterSession(claudeRow("restore-" + string(rune('a'+index)))); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		state, _, err := r.CatalogMirrorSnapshot(nil)
		if err != nil {
			t.Fatal(err)
		}
		return len(state.Sessions)
	}
	d.discoverCatalog(context.Background())
	if got := count(); got != catalogRestoreBatch {
		t.Fatalf("first pass restored %d rows", got)
	}
	d.discoverCatalog(context.Background())
	if got := count(); got != catalogRestoreBatch+3 {
		t.Fatalf("second pass left %d rows", got)
	}
}

func TestTheCurrentViewCountsHistoryWithoutListingIt(t *testing.T) {
	d, r, adapter := endedFixture(t)
	live, ended := claudeRow("live"), claudeRow("ended")
	adapter.sessions[live.ID], adapter.sessions[ended.ID] = live, ended
	d.discoverCatalog(context.Background())
	delete(adapter.sessions, ended.ID)
	for range 2 {
		d.discoverCatalog(context.Background())
	}
	current, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "current", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range current.Sessions {
		if row.ID == ended.ID {
			t.Fatal("history reached the current view")
		}
	}
	all, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if current.View.Counts["all"] != all.View.Counts["all"] {
		t.Fatalf("the All tab count depends on which tab is open: current read %+v, all read %+v", current.View.Counts["all"], all.View.Counts["all"])
	}
	listed := false
	for _, row := range all.Sessions {
		listed = listed || row.ID == ended.ID
	}
	if !listed {
		t.Fatalf("history is missing from All: %+v", all.Sessions)
	}
	if _, _, err := r.CatalogProjectionFingerprint(ended.ID); err != nil {
		t.Fatal(err)
	}
}
