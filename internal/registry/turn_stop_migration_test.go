package registry

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnUpgradedRegistryStopsWatchingTheOpenTurnsOfEndedSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, session := range []surface.Session{
		{ID: "ended-after-end", Status: surface.StatusOffline},
		{ID: "ended-mid-turn", Status: surface.StatusOffline},
		{ID: "live", Status: surface.StatusBusy},
	} {
		session.Surface, session.RuntimeKind, session.Cwd = surface.KindClaude, surface.RuntimeClaudeHost, "/work"
		if err := old.RecordObservedSession(session); err != nil {
			old.Close()
			t.Fatal(err)
		}
		if err := old.OpenTurn(session.ID, now.Add(-time.Minute)); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if _, err := old.EndTurn(TurnEnd{SessionID: "ended-after-end", Headline: "Opened the pull request", Source: TurnSourceTool}, "", now); err != nil {
		old.Close()
		t.Fatal(err)
	}
	if _, err := old.db.Exec(`DELETE FROM schema_migrations WHERE name='2026-11-26-your-workers-stopped-mid-turn-close-their-turn'`); err != nil {
		old.Close()
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	watched, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(watched) != 1 || watched[0].ID != "live" {
		t.Fatalf("watched after upgrade: %+v", watched)
	}
	if got := currentHeadline(t, r, "ended-after-end"); got != "Opened the pull request" {
		t.Fatalf("the last end of an ended session no longer stands: %q", got)
	}
}
