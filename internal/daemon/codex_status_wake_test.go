package daemon

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestACodexThreadThatStartsWaitingShowsBeforeTheNextDiscoveryPass(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h, _ := runDaemon(t, store, surfaces.NewCodex(unreachableDesktop))
	time.Sleep(1500 * time.Millisecond)
	if commands := lifecycle.Commands(); slices.Contains(commands, "app-server daemon start") {
		t.Fatalf("the daemon started Codex before any conversation needed it: %v", commands)
	}
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-create", "surface": "codex", "message": "Clean the build", "cwd": t.TempDir()})
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &created); status != http.StatusCreated || err != nil || created.Session.ID == "" {
		t.Fatalf("create status=%d body=%s err=%v", status, payload, err)
	}
	id := created.Session.ID
	testkit.Eventually(t, "the new thread to be listed", 10*time.Second, func() bool {
		status, payload := h.request(http.MethodGet, "/api/v1/snapshot", nil)
		return status == http.StatusOK && strings.Contains(string(payload), id)
	})
	if waiting := h.sessionWaiting(id); waiting != nil {
		t.Fatalf("the thread waits on %+v before Codex asks anything", waiting)
	}
	lifecycle.Daemon().SetActiveFlags(id, "waitingOnApproval")
	lifecycle.Daemon().Notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active", "activeFlags": []any{"waitingOnApproval"}}})
	testkit.Eventually(t, "the row to wait on the approval", 5*time.Second, func() bool {
		waiting := h.sessionWaiting(id)
		return waiting != nil && waiting.Kind == surface.WaitingApproval
	})
}
