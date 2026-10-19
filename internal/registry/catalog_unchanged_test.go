package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func totalChanges(t *testing.T, r *Registry) int64 {
	t.Helper()
	var changes int64
	if err := r.db.QueryRow(`SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	return changes
}

func unchangedCatalogState(id string, observedAt time.Time) (CatalogSessionState, CatalogEvent) {
	projection := `{"id":"` + id + `","surface":"codex","status":"idle"}`
	session := surface.Session{ID: id, Surface: surface.KindCodex, Name: "Worker", Cwd: "/work", Status: surface.StatusIdle, Transcript: "/work/transcript.jsonl", HasLocal: true, Source: "vscode", Transport: "desktop", LastActive: time.UnixMilli(1_790_000_000_000), Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	return CatalogSessionState{Session: session, Project: []byte(`{"id":"project"}`), Checkout: []byte(`{"id":"checkout"}`), ObservedAt: observedAt, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "session.upserted:" + id, Type: "session.upserted", EntityID: id, Payload: []byte(`{"session":` + projection + `}`)}
}

func TestRecordCatalogSessionWritesNothingForUnchangedRow(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	state, event := unchangedCatalogState("unchanged", observed)
	if _, created, err := r.RecordCatalogSession(state, event); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	before := totalChanges(t, r)
	state.ObservedAt = observed.Add(time.Minute)
	if _, created, err := r.RecordCatalogSession(state, event); err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if changes := totalChanges(t, r) - before; changes != 0 {
		t.Fatalf("unchanged row wrote %d rows", changes)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if row := snapshot.Sessions[0]; row.Freshness.Generation != 1 || !row.ObservedAt.Equal(observed) {
		t.Fatalf("freshness=%+v observedAt=%s", row.Freshness, row.ObservedAt)
	}
}

func TestRecordCatalogSessionRestoresRowChangedByAnotherWriter(t *testing.T) {
	r := openTestRegistry(t)
	state, event := unchangedCatalogState("rewritten", time.Now())
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
	other := state.Session
	other.Status = surface.StatusBusy
	other.Runtime = &surface.Runtime{Launcher: surface.LauncherTMUX, Location: &surface.Location{Session: "work", Pane: "%1"}, Focusable: true}
	if err := r.RegisterSession(other); err != nil {
		t.Fatal(err)
	}
	if _, created, err := r.RecordCatalogSession(state, event); err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	stored, err := r.Session(state.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != surface.StatusIdle || stored.Runtime == nil || stored.Runtime.Launcher != surface.LauncherExternal {
		t.Fatalf("stored=%+v runtime=%+v", stored, stored.Runtime)
	}
}

func TestRecordCatalogDiscoveryStampsOnlySeenFreshRows(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"seen", "unseen", "stale"} {
		state, event := unchangedCatalogState(id, observed)
		if _, _, err := r.RecordCatalogSession(state, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`UPDATE catalog_sessions SET discovery_failures=1 WHERE session_id='stale'`); err != nil {
		t.Fatal(err)
	}
	pass := observed.Add(30 * time.Second)
	seen := map[string]struct{}{"seen": {}, "stale": {}}
	if events, err := r.RecordCatalogDiscovery(surface.KindCodex, seen, pass, false, 2); err != nil || len(events) != 0 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Time{"seen": pass, "unseen": observed, "stale": observed}
	for _, row := range snapshot.Sessions {
		if !row.ObservedAt.Equal(want[row.Session.ID]) || !row.Freshness.ObservedAt.Equal(want[row.Session.ID]) {
			t.Fatalf("%s observedAt=%s want %s", row.Session.ID, row.ObservedAt, want[row.Session.ID])
		}
		if row.Session.ID == "unseen" && row.Freshness.Stale {
			t.Fatalf("incomplete discovery counted a miss for %s", row.Session.ID)
		}
	}
	if _, err := r.RecordCatalogDiscovery(surface.KindCodex, seen, pass.Add(30*time.Second), true, 2); err != nil {
		t.Fatal(err)
	}
	snapshot, err = r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range snapshot.Sessions {
		if row.Session.ID == "unseen" && (!row.Freshness.Stale || !row.ObservedAt.Equal(observed)) {
			t.Fatalf("complete discovery omission row=%+v", row.Freshness)
		}
	}
}

func TestDiscoveredBranchDetachesTheProcessAndLeavesTheOriginalItsAlias(t *testing.T) {
	r := openTestRegistry(t)
	original, originalEvent := unchangedCatalogState("conversation-a", time.Now())
	original.Session = surface.Session{ID: "conversation-a", Surface: surface.KindClaude, Cwd: "/work", PID: 77, Bridge: "session_bridge", Transcript: "/home/test/.claude/projects/-work/conversation-a.jsonl", HasLocal: true, Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	if _, _, err := r.RecordCatalogSession(original, originalEvent); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("lead", original.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(original.Session.ID, "for the original"); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := r.catalogSessionUnchanged(original, originalEvent); err != nil || !unchanged {
		t.Fatalf("unchanged=%v err=%v", unchanged, err)
	}
	branch, branchEvent := unchangedCatalogState("conversation-b", time.Now())
	branch.Session = surface.Session{ID: "conversation-b", Surface: surface.KindClaude, Cwd: "/work", PID: 77, Bridge: "session_bridge", ForkedFrom: "conversation-a", Transcript: "/home/test/.claude/projects/-work/conversation-b.jsonl", HasLocal: true, Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	if _, _, err := r.RecordCatalogSession(branch, branchEvent); err != nil {
		t.Fatal(err)
	}
	kept, err := r.Session("conversation-a")
	if err != nil || kept.PID != 0 || kept.Bridge != "" {
		t.Fatalf("original=%+v err=%v", kept, err)
	}
	forked, err := r.Session("conversation-b")
	if err != nil || forked.PID != 77 || forked.Bridge != "session_bridge" || forked.ForkedFrom != "conversation-a" {
		t.Fatalf("branch=%+v err=%v", forked, err)
	}
	if owner, err := r.LookupAlias("lead"); err != nil || owner != "conversation-a" {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
	if r.QueueCount("conversation-a") != 1 || r.QueueCount("conversation-b") != 0 {
		t.Fatalf("queue a=%d b=%d", r.QueueCount("conversation-a"), r.QueueCount("conversation-b"))
	}
	if unchanged, err := r.catalogSessionUnchanged(original, originalEvent); err != nil || unchanged {
		t.Fatalf("original still holding the branch's process counted as unchanged: %v %v", unchanged, err)
	}
}

func TestForkedFromIsSetOnce(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "conversation-b", Surface: surface.KindClaude, ForkedFrom: "conversation-a"}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	session.ForkedFrom = ""
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.Session(session.ID); err != nil || stored.ForkedFrom != "conversation-a" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}
