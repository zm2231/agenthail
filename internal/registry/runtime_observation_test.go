package registry

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestLauncherMetadataIsNotAnObservedTurnBaseline(t *testing.T) {
	reg, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	launched := surface.Session{ID: "launched", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Runtime: &surface.Runtime{Launcher: "tmux", Focusable: true}}
	if err := reg.RecordObservedSession(launched); err != nil {
		t.Fatal(err)
	}
	if _, found, err := reg.RuntimeState("launched"); err != nil || found {
		t.Fatalf("launcher metadata reported an observed turn: found=%v err=%v", found, err)
	}
	if err := reg.MarkDeliveryStarted("launched", "turn-new", "turn-before-delivery"); err != nil {
		t.Fatal(err)
	}
	state, found, err := reg.RuntimeState("launched")
	if err != nil || !found || state.CompletedTurnID != "turn-before-delivery" || state.ActiveTurnID != "turn-new" {
		t.Fatalf("delivery baseline found=%v err=%v state=%+v", found, err, state)
	}
	if runtime, ok, err := reg.SessionRuntime("launched"); err != nil || !ok || runtime.Launcher != "tmux" {
		t.Fatalf("launcher metadata lost: runtime=%+v ok=%v err=%v", runtime, ok, err)
	}
}

func TestUpgradeKeepsObservedRuntimeAndDropsLauncherPlaceholders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.RecordObservedSession(surface.Session{ID: "observed", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}); err != nil {
		t.Fatal(err)
	}
	if err := old.SaveRuntimeState("observed", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	if err := old.RecordObservedSession(surface.Session{ID: "launched", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Runtime: &surface.Runtime{Launcher: "cmux"}}); err != nil {
		t.Fatal(err)
	}
	asBeforePresentationTriggers(t, old)
	for _, statement := range []string{`ALTER TABLE session_runtime DROP COLUMN turn_observed`, fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion-1)} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if state, found, err := upgraded.RuntimeState("observed"); err != nil || !found || state.CompletedTurnID != "turn-1" {
		t.Fatalf("observed runtime lost on upgrade: found=%v err=%v state=%+v", found, err, state)
	}
	if _, found, err := upgraded.RuntimeState("launched"); err != nil || found {
		t.Fatalf("launcher placeholder still reported as observed: found=%v err=%v", found, err)
	}
}

// Readers keep their page cache only while no other connection commits, so a
// scan that saves an unchanged observation a few seconds after the last must
// not commit a write.
func TestSavingAnUnchangedObservationLeavesReadersCachesValid(t *testing.T) {
	reg := coordinatorRegistry(t, "worker")
	ctx := context.Background()
	reader, err := reg.read.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	version := func() int64 {
		t.Helper()
		var v int64
		if err := reader.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	observed := surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}
	if err := reg.SaveRuntimeState("worker", observed); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.db.Exec(`UPDATE session_runtime SET updated_at=datetime('now','-5 seconds') WHERE session_id='worker'`); err != nil {
		t.Fatal(err)
	}
	before := version()
	if err := reg.SaveRuntimeState("worker", observed); err != nil {
		t.Fatal(err)
	}
	if after := version(); after != before {
		t.Fatalf("an unchanged observation committed a write: data_version %d -> %d", before, after)
	}
	observed.Status, observed.ActiveTurnID = surface.StatusBusy, "turn-2"
	if err := reg.SaveRuntimeState("worker", observed); err != nil {
		t.Fatal(err)
	}
	if after := version(); after == before {
		t.Fatal("a changed observation was not written")
	}
	state, found, err := reg.RuntimeState("worker")
	if err != nil || !found || state.LastStatus != surface.StatusBusy || state.ActiveTurnID != "turn-2" || state.CompletedTurnID != "turn-1" {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
}
