package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
)

func TestAnEndedAppServerThreadIsReloadedWithoutStartingATurn(t *testing.T) {
	fake := startManagedCodex(t, func(string, map[string]any) map[string]any { return nil })
	fake.AddThread(codextest.Thread{ID: "thread", Originator: "agenthail", Source: "agenthail"})
	codex := isolatedManagedRuntime(t)
	session := managedSession()
	if !codex.Ended(context.Background(), session) {
		t.Fatal("an unloaded thread does not read as ended")
	}
	if err := codex.Wake(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if !fake.Loaded("thread") || len(fake.Calls("thread/resume")) != 1 {
		t.Fatalf("loaded=%v resumes=%d", fake.Loaded("thread"), len(fake.Calls("thread/resume")))
	}
	if turns := fake.Calls("turn/start"); len(turns) != 0 {
		t.Fatalf("bringing the thread back started a turn: %v", turns)
	}
	if codex.Ended(context.Background(), session) {
		t.Fatal("a reloaded thread still reads as ended")
	}
}
