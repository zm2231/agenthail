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
	movesFrom := func(previous string) func() bool {
		return func() bool { rev := h.decisionsRev(); return rev != "" && rev != previous }
	}

	codex := lifecycle.Daemon()
	codex.OpenRequest(7, "item/commandExecution/requestApproval", codexApproval(id, "call_a"))
	codex.Notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active", "activeFlags": []any{"waitingOnApproval"}}})
	testkit.Eventually(t, "the row to wait on the one request", 10*time.Second, opens(first))
	testkit.Eventually(t, "a decisions revision", 5*time.Second, movesFrom(""))
	one := h.decisionsRev()

	codex.OpenRequest(8, "item/commandExecution/requestApproval", codexApproval(id, "call_b"))
	testkit.Eventually(t, "the row to list the second request", 10*time.Second, opens(first, second))
	testkit.Eventually(t, "the decisions revision to move for the second request", 5*time.Second, movesFrom(one))
	two := h.decisionsRev()

	time.Sleep(2500 * time.Millisecond)
	if !opens(first, second)() || h.decisionsRev() != two {
		t.Fatalf("status passes changed a settled wait: waiting=%+v rev %s -> %s", h.sessionWaiting(id), two, h.decisionsRev())
	}

	codex.Answer(8)
	testkit.Eventually(t, "the row to drop the request answered elsewhere", 10*time.Second, opens(first))
	testkit.Eventually(t, "the decisions revision to move for the answer", 5*time.Second, movesFrom(two))
}
