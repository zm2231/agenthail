package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

type desktopAlert struct{ message, sessionID string }

type desktopRecorder struct {
	mu     sync.Mutex
	alerts []desktopAlert
}

func (r *desktopRecorder) notify(_, message, sessionID, _, _ string, _ []surface.RequestAction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alerts = append(r.alerts, desktopAlert{message: message, sessionID: sessionID})
	return nil
}

func (r *desktopRecorder) matching(suffix string) []desktopAlert {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []desktopAlert
	for _, alert := range r.alerts {
		if strings.HasSuffix(alert.message, suffix) {
			out = append(out, alert)
		}
	}
	return out
}

func (h *runningDaemon) catalogRow(id string) (dashboardSession, bool) {
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
			return session, true
		}
	}
	return dashboardSession{}, false
}

func TestCodexSubagentTurnsRaiseNoCompletionAlertAndItsApprovalOpensTheRoot(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	pushes := newPushRecorder(t, store)
	desktop := &desktopRecorder{}
	h, _ := runDaemonWith(t, store, func(d *Daemon) { d.notifyDesktop = desktop.notify }, surfaces.NewCodex(unreachableDesktop))

	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-create", "surface": "codex", "message": "Plan the release", "cwd": t.TempDir()})
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &created); status != http.StatusCreated || err != nil || created.Session.ID == "" {
		t.Fatalf("create status=%d body=%s err=%v", status, payload, err)
	}
	root := created.Session.ID
	testkit.Eventually(t, "the root's first turn", 5*time.Second, func() bool {
		thread, _ := lifecycle.Daemon().Thread(root)
		return len(thread.Turns) == 1
	})
	lifecycle.Daemon().CompleteTurn(root, "Release plan ready")

	child := "019f004a-0000-7000-8000-0000000000c1"
	lifecycle.Daemon().AddThread(codextest.Thread{ID: child, Cwd: t.TempDir(), Originator: "agenthail", Source: map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": root, "depth": 1, "agent_path": nil, "agent_nickname": "Ada", "agent_role": nil}}}})
	testkit.Eventually(t, "the subagent's discovery", 5*time.Second, func() bool {
		h.daemon.requestCatalogDiscovery()
		session, err := store.Session(child)
		return err == nil && session.Subagent != nil && session.Subagent.RootID == root
	})
	if row, _ := h.catalogRow(root); row.Delegated {
		t.Fatalf("the user's own conversation is listed as delegated: %+v", row)
	}
	// A relay between the two keeps both observed; its pattern never matches,
	// so no reply is relayed.
	if _, err := store.AddRoute(child, root, "^relay-never-matches$"); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "both sessions to be observed", 5*time.Second, func() bool {
		_, rootSeen, _ := store.RuntimeState(root)
		_, childSeen, _ := store.RuntimeState(child)
		return rootSeen && childSeen
	})

	h.receipt(map[string]any{"action": "send", "sessionId": root, "message": "Ship it"})
	testkit.Eventually(t, "the root's second turn", 5*time.Second, func() bool {
		thread, _ := lifecycle.Daemon().Thread(root)
		return len(thread.Turns) == 2
	})
	testkit.Eventually(t, "the root's second turn to be observed", 5*time.Second, func() bool {
		state, _, _ := store.RuntimeState(root)
		return state.ActiveTurnID == "turn-2"
	})
	lifecycle.Daemon().CompleteTurn(root, "Shipped")
	testkit.Eventually(t, "the root's completion alert", 5*time.Second, func() bool {
		return pushes.count("turn.completed") == 1 && len(desktop.matching(" finished")) == 1
	})

	h.receipt(map[string]any{"action": "send", "sessionId": child, "message": "Check the changelog"})
	testkit.Eventually(t, "the subagent's turn", 5*time.Second, func() bool {
		thread, _ := lifecycle.Daemon().Thread(child)
		return len(thread.Turns) == 1
	})
	testkit.Eventually(t, "the working subagent's catalog row", 5*time.Second, func() bool {
		h.daemon.requestCatalogDiscovery()
		row, found := h.catalogRow(child)
		return found && row.Delegated
	})
	lifecycle.Daemon().SetActiveFlags(child, "waitingOnApproval")
	testkit.Eventually(t, "the subagent's approval alert", 5*time.Second, func() bool {
		return pushes.count("session.waiting") == 1 && len(desktop.matching(" needs your approval")) == 1
	})
	if push := pushes.first("session.waiting"); push["sessionId"] != root {
		t.Fatalf("the approval push opens %q, not the root %q", push["sessionId"], root)
	}
	if alert := desktop.matching(" needs your approval")[0]; alert.sessionID != root {
		t.Fatalf("the desktop approval alert opens %q, not the root %q", alert.sessionID, root)
	}

	lifecycle.Daemon().SetActiveFlags(child)
	lifecycle.Daemon().CompleteTurn(child, "Changelog checked")
	testkit.Eventually(t, "the subagent's completion to be observed", 5*time.Second, func() bool {
		state, found, err := store.RuntimeState(child)
		return err == nil && found && state.CompletedTurnID != "" && state.ActiveTurnID == ""
	})
	// A later root turn's alert is the barrier: the subagent's turn finished
	// before it, so any alert for it would already have been sent.
	h.receipt(map[string]any{"action": "send", "sessionId": root, "message": "Tag the release"})
	testkit.Eventually(t, "the root's third turn to be observed", 5*time.Second, func() bool {
		state, _, _ := store.RuntimeState(root)
		return state.ActiveTurnID == "turn-3"
	})
	lifecycle.Daemon().CompleteTurn(root, "Tagged")
	testkit.Eventually(t, "the root's second completion alert", 5*time.Second, func() bool {
		return pushes.count("turn.completed") >= 2 && len(desktop.matching(" finished")) >= 2
	})
	for _, push := range pushes.all("turn.completed") {
		if push["sessionId"] != root {
			t.Fatalf("a completion push for %q; only the root's turns alert", push["sessionId"])
		}
	}
	for _, alert := range desktop.matching(" finished") {
		if alert.sessionID != root {
			t.Fatalf("a desktop completion alert for %q; only the root's turns alert", alert.sessionID)
		}
	}
}
