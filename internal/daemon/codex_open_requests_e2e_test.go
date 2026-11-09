package daemon

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func (h *runningDaemon) decisionsRev() string {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/snapshot", nil)
	var decoded struct {
		DecisionsRev string `json:"decisionsRev"`
	}
	if status != http.StatusOK || json.Unmarshal(payload, &decoded) != nil {
		h.t.Fatalf("snapshot status=%d body=%s", status, payload)
	}
	return decoded.DecisionsRev
}

func codexApproval(threadID, itemID string) map[string]any {
	return map[string]any{
		"threadId": threadID, "turnId": "turn-1", "itemId": itemID, "command": "rm -rf " + itemID, "cwd": "/work",
		"startedAtMs": float64(time.Now().UnixMilli()), "availableDecisions": []any{"accept", "decline", "cancel"},
	}
}

func TestEveryCodexRequestOpeningOrClosingMovesTheRowAndTheDecisionsRevision(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h, _ := runDaemon(t, store, surfaces.NewCodex(unreachableDesktop))
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
	first, second := id+":call_a", id+":call_b"
	opens := func(want ...string) func() bool {
		return func() bool {
			waiting := h.sessionWaiting(id)
			return waiting != nil && waiting.ID == want[0] && slices.Equal(waiting.Open, want)
		}
	}
	// The revision settles one pass after the row changes.
	caughtUp := func(what, previous string, want ...string) string {
		var rev string
		testkit.Eventually(t, what, 10*time.Second, func() bool {
			if !opens(want...)() {
				return false
			}
			own, err := h.daemon.ownDecisionsRevision(t.Context())
			rev = h.decisionsRev()
			return err == nil && rev == own && rev != previous && opens(want...)()
		})
		return rev
	}

	codex := lifecycle.Daemon()
	codex.OpenRequest(7, "item/commandExecution/requestApproval", codexApproval(id, "call_a"))
	codex.Notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active", "activeFlags": []any{"waitingOnApproval"}}})
	one := caughtUp("the row and decisions revision to wait on the one request", "", first)

	codex.OpenRequest(8, "item/commandExecution/requestApproval", codexApproval(id, "call_b"))
	two := caughtUp("the row and decisions revision to list the second request", one, first, second)

	time.Sleep(2500 * time.Millisecond)
	if !opens(first, second)() || h.decisionsRev() != two {
		t.Fatalf("status passes changed a settled wait: waiting=%+v rev %s -> %s", h.sessionWaiting(id), two, h.decisionsRev())
	}

	codex.Answer(8)
	caughtUp("the row and decisions revision to drop the request answered elsewhere", two, first)
}
