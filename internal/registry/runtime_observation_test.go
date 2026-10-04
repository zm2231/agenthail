package registry

import (
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
	launched := surface.Session{ID: "launched", Surface: surface.KindCodex, Runtime: &surface.Runtime{Launcher: "tmux", Focusable: true}}
	if err := reg.RegisterSession(launched); err != nil {
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
	if err := old.RegisterSession(surface.Session{ID: "observed", Surface: surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	if err := old.SaveRuntimeState("observed", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	if err := old.RegisterSession(surface.Session{ID: "launched", Surface: surface.KindCodex, Runtime: &surface.Runtime{Launcher: "cmux"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`ALTER TABLE session_runtime DROP COLUMN turn_observed`); err != nil {
		old.Close()
		t.Fatal(err)
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
