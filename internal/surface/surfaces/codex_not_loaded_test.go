package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAThreadCodexHasNotLoadedListsAsNotLoadedAndEnded(t *testing.T) {
	for name, turns := range map[string][]codextest.Turn{
		"no turns":         nil,
		"a completed turn": {{ID: "turn-1", Status: "completed", User: "hi", Assistant: "hello"}},
	} {
		t.Run(name, func(t *testing.T) {
			fake := startManagedCodex(t, nil)
			fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Turns: turns})
			codex := isolatedManagedRuntime(t)
			sessions, err := codex.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var listed *surface.Session
			for i := range sessions {
				if sessions[i].ID == managedThreadID {
					listed = &sessions[i]
				}
			}
			if listed == nil || listed.Status != surface.StatusNotLoaded || !listed.Ended {
				t.Fatalf("listed=%+v", listed)
			}
		})
	}
}
