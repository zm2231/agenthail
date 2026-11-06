package surfaces

import (
	"context"
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
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

func TestAnAppServerThreadDesktopHasLoadedIsLeftToDesktop(t *testing.T) {
	fake := startManagedCodex(t, func(string, map[string]any) map[string]any { return nil })
	fake.AddThread(codextest.Thread{ID: "thread", Originator: "agenthail", Source: "agenthail"})
	desktop := startDesktopBridge(t, func(method string) string {
		if method == "thread/loaded/list" {
			return `{"result":{"data":["thread"]}}`
		}
		return ""
	})
	codex := NewCodex(desktop.URL)
	session := managedSession()
	if codex.Ended(context.Background(), session) {
		t.Fatal("a thread Desktop has loaded reads as ended")
	}
	if err := codex.Wake(context.Background(), session); !errors.Is(err, surface.ErrDeliveryHeld) {
		t.Fatalf("err=%v, want Desktop to hold it", err)
	}
	for _, method := range desktop.Methods() {
		if method != "thread/loaded/list" {
			t.Fatalf("the keeper sent %s to Desktop", method)
		}
	}
	for _, method := range []string{"thread/read", "thread/resume", "turn/start"} {
		if calls := fake.Calls(method); len(calls) != 0 {
			t.Fatalf("the app-server received %s", method)
		}
	}
}
