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
	return CatalogSessionState{Session: session, HostProject: []byte(`{"id":"project"}`), Checkout: []byte(`{"id":"checkout"}`), ObservedAt: observedAt, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "session.upserted:" + id, Type: "session.upserted", EntityID: id, Payload: []byte(`{"session":` + projection + `}`)}
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

func TestDiscoveredClaudeAgentAbsorbsItsLaunchRecord(t *testing.T) {
	r := openTestRegistry(t)
	transcript := "/home/test/.claude/projects/-work/conversation-1.jsonl"
	launch := surface.Session{ID: "conversation-1", Surface: surface.KindClaude, Name: "builder", Cwd: "/work", Status: surface.StatusUnknown, HasLocal: true, Source: "agenthail"}
	if err := r.RegisterSession(launch); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("builder", launch.ID); err != nil {
		t.Fatal(err)
	}
	state, event := unchangedCatalogState("session_bridge", time.Now())
	state.Session = surface.Session{ID: "session_bridge", Surface: surface.KindClaude, Name: "builder", Cwd: "/work", PID: 4242, Status: surface.StatusBusy, Transcript: transcript, HasLocal: true, Transport: "uds", Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Session(launch.ID); err == nil {
		t.Fatal("launch record survived discovery of the same agent")
	}
	if owner, err := r.LookupAlias("builder"); err != nil || owner != "session_bridge" {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
}

func TestDiscoveredClaudeAgentKeepsUnrelatedConversationRows(t *testing.T) {
	r := openTestRegistry(t)
	live := surface.Session{ID: "conversation-2", Surface: surface.KindClaude, Name: "other", Cwd: "/work", PID: 99, Status: surface.StatusIdle}
	if err := r.RegisterSession(live); err != nil {
		t.Fatal(err)
	}
	discovered := surface.Session{ID: "session_bridge", Surface: surface.KindClaude, Cwd: "/work", PID: 4242, Transcript: "/home/test/.claude/projects/-work/conversation-2.jsonl", HasLocal: true}
	if err := r.RegisterSession(discovered); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Session(live.ID); err != nil {
		t.Fatalf("row with a live process was merged: %v", err)
	}
}

func TestDiscoveredClaudeAgentAbsorbsLaunchRegisteredAfterIt(t *testing.T) {
	r := openTestRegistry(t)
	transcript := "/home/test/.claude/projects/-work/conversation-3.jsonl"
	state, event := unchangedCatalogState("session_bridge", time.Now())
	state.Session = surface.Session{ID: "session_bridge", Surface: surface.KindClaude, Cwd: "/work", PID: 4242, Status: surface.StatusBusy, Transcript: transcript, HasLocal: true, Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "conversation-3", Surface: surface.KindClaude, Cwd: "/work", Status: surface.StatusUnknown, Source: "agenthail"}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("late", "conversation-3"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Session("conversation-3"); err == nil {
		t.Fatal("next discovery pass kept the launch record")
	}
	if owner, err := r.LookupAlias("late"); err != nil || owner != "session_bridge" {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
}

func TestMergedSessionKeepsDeliveryIntentsAndLeavesTheCatalog(t *testing.T) {
	stubLiveProcesses(t, 77)
	r := openTestRegistry(t)
	transcript := "/home/test/.claude/projects/-work/conversation-4.jsonl"
	legacy, legacyEvent := unchangedCatalogState("conversation-4", time.Now())
	legacy.Session = surface.Session{ID: "conversation-4", Surface: surface.KindClaude, Cwd: "/work", PID: 77, Transcript: transcript, HasLocal: true, Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	if _, _, err := r.RecordCatalogSession(legacy, legacyEvent); err != nil {
		t.Fatal(err)
	}
	other := surface.Session{ID: "other", Surface: surface.KindCodex}
	if err := r.RegisterSession(other); err != nil {
		t.Fatal(err)
	}
	inbound, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: other.ID, TargetSessionID: legacy.Session.ID, Message: "to the agent", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	outbound, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: legacy.Session.ID, TargetSessionID: other.ID, Message: "from the agent", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	_, latest, err := r.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	bridge, bridgeEvent := unchangedCatalogState("session_bridge", time.Now())
	bridge.Session = surface.Session{ID: "session_bridge", Surface: surface.KindClaude, Cwd: "/work", PID: 77, Transcript: transcript, HasLocal: true, Runtime: &surface.Runtime{Launcher: surface.LauncherExternal}}
	if _, _, err := r.RecordCatalogSession(bridge, bridgeEvent); err != nil {
		t.Fatal(err)
	}
	if moved, err := r.DeliveryIntent(inbound.ID); err != nil || moved.TargetSessionID != "session_bridge" {
		t.Fatalf("inbound=%+v err=%v", moved, err)
	}
	if moved, err := r.DeliveryIntent(outbound.ID); err != nil || moved.SenderSessionID != "session_bridge" {
		t.Fatalf("outbound=%+v err=%v", moved, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Session.ID != "session_bridge" {
		t.Fatalf("snapshot=%+v err=%v", snapshot.Sessions, err)
	}
	window, err := r.CatalogEventsAfter(latest, 20)
	if err != nil {
		t.Fatal(err)
	}
	removed := false
	for _, event := range window.Events {
		removed = removed || (event.Type == "session.removed" && event.EntityID == "conversation-4")
	}
	if !removed {
		t.Fatalf("events=%+v", window.Events)
	}
}
