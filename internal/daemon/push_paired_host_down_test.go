package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAPromptHeldOnAHostReachesItsPhoneThroughTheRelayWhileItsPairedHostIsDown(t *testing.T) {
	allowLoopbackHosts(t)
	q := newClaudeQuestion(t)
	var mu sync.Mutex
	var reached []string
	down := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached = append(reached, r.Method+" "+r.URL.Path)
		mu.Unlock()
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	if _, err := q.store.AddHost(registry.PairedHost{ID: "h0123456789ab", Name: "peer", Endpoint: down.URL, Address: "127.0.0.1", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	h, _ := runDaemonWith(t, q.store, func(d *Daemon) { d.federation.rootCAs = testRoots(down) }, q.claude)
	testkit.Eventually(t, "the question the record shows to be announced", 5*time.Second, func() bool { return q.pushes.count("session.waiting") == 1 })
	testkit.Eventually(t, "the daemon to have tried its paired host", 10*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reached) > 0
	})

	prompt := `{"session_id":"conversation-question","tool_name":"AskUserQuestion","tool_input":` + pickABranch + `}`
	answered := make(chan ClaudePermissionResponse, 1)
	go func() {
		_, response := h.postPrompt(prompt, q.process.PID)
		answered <- response
	}()
	var waiting *surface.Waiting
	testkit.Eventually(t, "the session to wait on the held prompt", 5*time.Second, func() bool {
		waiting = h.sessionWaiting(q.session.ID)
		return waiting != nil && strings.Contains(askRef(q.store, waiting.ID), ":held:")
	})
	testkit.Eventually(t, "the held prompt to reach the relay", 5*time.Second, func() bool { return q.pushes.count("session.waiting") == 2 })
	announced := q.pushes.all("session.waiting")[1]
	if announced["requestId"] != waiting.ID || announced["installationId"] != "installation" {
		t.Fatalf("the relay got %+v, want request %s for the host's own paired phone", announced, waiting.ID)
	}
	mu.Lock()
	for _, request := range reached {
		if strings.Contains(request, "push") || strings.Contains(request, "notif") || strings.Contains(request, "requests") {
			mu.Unlock()
			t.Fatalf("the prompt's announcement went to the paired host: %s", request)
		}
	}
	mu.Unlock()

	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": q.session.ID, "requestId": waiting.ID, "decision": map[string]any{"answers": map[string]string{"Which branch?": "next"}}})
	if status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
	select {
	case response := <-answered:
		if response.Decision == nil || response.Decision.Behavior != "allow" {
			t.Fatalf("the hook got %+v, want the answer", response.Decision)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hook never got the answer")
	}
}
