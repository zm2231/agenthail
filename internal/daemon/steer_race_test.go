package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

// The fake ends the active turn just before Agenthail's steer reads it: the
// send saw the thread busy, and the steer then finds nothing to steer.
func TestBusySteerWhoseTurnEndsFirstIsSentAsTheNextTurn(t *testing.T) {
	fixture := &headlessCodexFixture{}
	var mu sync.Mutex
	thread, armed, turnLists := "", false, 0
	fixture.lifecycle = codextest.InstallLifecycle(t, codextest.Home(t), func(method string, _ map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		if armed && method == "thread/turns/list" {
			if turnLists++; turnLists == 2 {
				fixture.lifecycle.Daemon().CompleteTurn(thread, "finished")
			}
		}
		return nil
	})
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fixture.registry = r
	fixture.restartAgenthail(unreachableDesktop)
	code, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First task", "cwd": t.TempDir()})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if code >= 300 || id == "" {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	mu.Lock()
	thread, armed = id, true
	mu.Unlock()

	code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Second task", "busyDelivery": "steer"})
	result, _ := body["result"].(map[string]any)
	if code != http.StatusOK || result["evidence"] != string(surface.EvidenceDelivered) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	got := fixture.thread(t, id)
	if len(got.Turns) != 2 || got.Turns[0].User != "First task" || got.Turns[1].User != "Second task" || len(fixture.lifecycle.Daemon().Calls("turn/steer")) != 0 {
		t.Fatalf("thread=%+v steers=%d", got, len(fixture.lifecycle.Daemon().Calls("turn/steer")))
	}
	if r.QueueCount(id) != 0 {
		t.Fatalf("queued=%d", r.QueueCount(id))
	}
}

func TestQueuedSteerWhoseTurnEndsFirstIsSentAsTheNextTurn(t *testing.T) {
	fixture := &headlessCodexFixture{}
	var mu sync.Mutex
	thread, armed, turnLists := "", false, 0
	fixture.lifecycle = codextest.InstallLifecycle(t, codextest.Home(t), func(method string, _ map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		if armed && method == "thread/turns/list" {
			if turnLists++; turnLists == 2 {
				fixture.lifecycle.Daemon().CompleteTurn(thread, "finished")
			}
		}
		return nil
	})
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fixture.registry = r
	fixture.restartAgenthail(unreachableDesktop)
	code, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First task", "cwd": t.TempDir()})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if code >= 300 || id == "" {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	if _, err := r.QueueMessageWithOptions(id, "Second task", "", surface.SendOptions{BusyDelivery: "steer"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	thread, armed = id, true
	mu.Unlock()

	New(r, []surface.Surface{surfaces.NewCodex(unreachableDesktop)}).scanAndRelay(context.Background())
	got := fixture.thread(t, id)
	if len(got.Turns) != 2 || got.Turns[1].User != "Second task" || len(fixture.lifecycle.Daemon().Calls("turn/steer")) != 0 || r.QueueCount(id) != 0 {
		t.Fatalf("thread=%+v steers=%d queued=%d", got, len(fixture.lifecycle.Daemon().Calls("turn/steer")), r.QueueCount(id))
	}
}
