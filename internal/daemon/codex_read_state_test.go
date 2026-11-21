package daemon

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func (h *runningDaemon) sessionReadState(id string) *surface.ReadState {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/snapshot", nil)
	if status != http.StatusOK {
		h.t.Fatalf("snapshot status=%d body=%s", status, payload)
	}
	var decoded struct {
		Sessions []dashboardSession `json:"sessions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatal(err)
	}
	for _, session := range decoded.Sessions {
		if session.ID == id {
			return session.ReadState
		}
	}
	return nil
}

func TestAFinishedCodexTurnShowsUnreadUntilThePersonReadsIt(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var d *Daemon
	h, _ := runDaemonWith(t, store, func(daemon *Daemon) { d = daemon }, surfaces.NewCodex(unreachableDesktop))
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
	lifecycle.Daemon().CompleteTurn(id, "Built")
	var unread *surface.ReadState
	testkit.Eventually(t, "the finished turn to show unread", 10*time.Second, func() bool {
		unread = h.sessionReadState(id)
		return unread != nil && unread.Unread()
	})

	agent := d.performAction(t.Context(), sessionPrincipal("other"), actionFormatAPIv1, mustJSON(t, map[string]any{"action": "session-read", "sessionId": id, "revision": unread.Revision}), "agent-read")
	if agent.Status != http.StatusForbidden {
		t.Fatalf("an agent's read status=%d body=%s, want 403", agent.Status, agent.Body)
	}
	forwarded := d.performAction(t.Context(), principal{ID: "host:peer", Kind: principalHost, Claimed: "session other"}, actionFormatAPIv1, mustJSON(t, map[string]any{"action": "session-read", "sessionId": id, "revision": unread.Revision}), "forwarded-agent-read")
	if forwarded.Status != http.StatusForbidden {
		t.Fatalf("an agent's read forwarded by a paired host status=%d body=%s, want 403", forwarded.Status, forwarded.Body)
	}

	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-read", "sessionId": id, "revision": unread.Revision})
	if status != http.StatusOK {
		t.Fatalf("read status=%d body=%s", status, payload)
	}
	testkit.Eventually(t, "the row to show read", 5*time.Second, func() bool {
		state := h.sessionReadState(id)
		return state != nil && !state.Unread() && state.Revision != unread.Revision
	})
	if receipt := lifecycle.Daemon().ReadState(id); receipt["firstUnread"] != nil {
		t.Fatalf("Codex receipt = %v, want read", receipt)
	}
	read := h.sessionReadState(id)
	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-read", "sessionId": id, "revision": read.Revision})
	if status != http.StatusOK {
		t.Fatalf("reopen status=%d body=%s", status, payload)
	}
	if updates := len(lifecycle.Daemon().Calls("thread/readState/update")); updates != 1 {
		t.Fatalf("reopening a read conversation marked it again: %d updates", updates)
	}

	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-unread", "sessionId": id, "revision": unread.Revision})
	if status != http.StatusConflict {
		t.Fatalf("a mark at a stale revision status=%d body=%s, want 409", status, payload)
	}
	if updates := len(lifecycle.Daemon().Calls("thread/readState/update")); updates != 2 {
		t.Fatalf("thread/readState/update calls=%d, want 2 with no retry", updates)
	}
	if state := h.sessionReadState(id); state == nil || state.Unread() {
		t.Fatalf("a refused mark changed the row: %+v", state)
	}
}
