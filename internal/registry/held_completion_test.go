package registry

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func watchedIDs(t *testing.T, r *Registry) []string {
	t.Helper()
	sessions, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

func TestAnUpgradedRegistryHoldsACompletionAndWatchesItsSessionUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.RegisterSession(surface.Session{ID: "codex", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	if err := old.SaveRuntimeState("codex", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`ALTER TABLE session_runtime DROP COLUMN held_completion_turn_id`, fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion-1)} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if state, found, err := r.RuntimeState("codex"); err != nil || !found || state.CompletedTurnID != "turn-1" || state.HeldCompletionTurnID != "" {
		t.Fatalf("upgraded state=%+v found=%v err=%v", state, found, err)
	}
	if slices.Contains(watchedIDs(t, r), "codex") {
		t.Fatal("a session holding nothing is watched")
	}
	if err := r.HoldCompletion("codex", "turn-1"); err != nil {
		t.Fatal(err)
	}
	if state, _, err := r.RuntimeState("codex"); err != nil || state.HeldCompletionTurnID != "turn-1" {
		t.Fatalf("held state=%+v err=%v", state, err)
	}
	if !slices.Contains(watchedIDs(t, r), "codex") {
		t.Fatal("a session holding a completion is not watched")
	}
	if err := r.HoldCompletion("codex", ""); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(watchedIDs(t, r), "codex") {
		t.Fatal("a released session is still watched")
	}
}
