package daemon

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestACodexApprovalIsAnAnswerableDecisionAndItsAnswerReachesCodex(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h, stop := runDaemon(t, store, surfaces.NewCodex(unreachableDesktop))
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
	lifecycle.Daemon().OpenRequest(7, "item/commandExecution/requestApproval", map[string]any{
		"threadId": id, "turnId": "turn-1", "itemId": "call_1", "command": "rm -rf build", "cwd": "/work",
		"startedAtMs": float64(time.Now().UnixMilli()), "availableDecisions": []any{"accept", "acceptForSession", "decline", "cancel"},
	})
	stop()
	h, _ = runDaemon(t, store, surfaces.NewCodex(unreachableDesktop))

	type decision struct {
		SessionID  string `json:"sessionId"`
		Group      string `json:"group"`
		Answerable bool   `json:"answerable"`
		Requests   []struct {
			ID      string                  `json:"id"`
			Prompt  string                  `json:"prompt"`
			Actions []surface.RequestAction `json:"actions"`
		} `json:"requests"`
		RequestError string `json:"requestError"`
	}
	decisions := func() []decision {
		status, payload := h.request(http.MethodGet, "/api/v1/decisions", nil)
		var page struct {
			Decisions []decision `json:"decisions"`
		}
		if status != http.StatusOK || json.Unmarshal(payload, &page) != nil {
			return nil
		}
		var mine []decision
		for _, item := range page.Decisions {
			if item.SessionID == id {
				mine = append(mine, item)
			}
		}
		return mine
	}
	var card decision
	testkit.Eventually(t, "the approval to be an answerable decision", 10*time.Second, func() bool {
		found := decisions()
		if len(found) != 1 || !found[0].Answerable {
			return false
		}
		card = found[0]
		return true
	})
	requestID := id + ":call_1"
	var actions []string
	for _, action := range card.Requests[0].Actions {
		actions = append(actions, action.ID)
	}
	if card.Group != decisionWaiting || askRef(store, card.Requests[0].ID) != requestID || card.Requests[0].Prompt != "rm -rf build" || !reflect.DeepEqual(actions, []string{"allow", "always", "deny", "stop"}) {
		t.Fatalf("decision %+v actions %v", card, actions)
	}
	if waiting := h.sessionWaiting(id); waiting == nil || askRef(store, waiting.ID) != requestID || !waiting.Answerable {
		t.Fatalf("the row waits on %+v, want the request %s", waiting, requestID)
	}

	askID := card.Requests[0].ID
	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": id, "requestId": askID, "decision": map[string]any{"choice": "always"}})
	if status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
	var answered map[string]any
	for _, response := range lifecycle.Daemon().Responses() {
		if response["id"] == float64(7) {
			answered, _ = response["result"].(map[string]any)
		}
	}
	if answered["decision"] != "acceptForSession" {
		t.Fatalf("Codex was answered %v", answered)
	}
	testkit.Eventually(t, "the decision to leave the feed", 10*time.Second, func() bool { return len(decisions()) == 0 })
	status, _ = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": id, "requestId": askID, "decision": map[string]any{"behavior": "allow"}})
	if status != http.StatusConflict {
		t.Fatalf("a second answer: status %d, want a stale conflict", status)
	}
}
