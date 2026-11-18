package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestANameTwoSessionsOnAPairedHostMatchIsRefusedAsInvalid(t *testing.T) {
	l := leadOnTheOtherHost(t)
	moveSession(t, l.laptop, "laptop-gadget", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	lead, err := l.vps.reg.Session("vps-a")
	if err != nil {
		t.Fatal(err)
	}
	client := mcpSession(t, mcpServer(t, l.vps.daemon), issueMCPToken(t, l.vps.daemon, *lead))
	out, refused := callMCP(t, client, "agenthail_send", map[string]any{"target": "laptop", "message": "hello", "mode": "queue"})
	if !refused || out["code"] != "invalid" || out["retryable"] == true || !strings.Contains(out["message"].(string), "ambiguous") {
		t.Fatalf("a name two sessions on the paired host match: %v, want it refused as invalid and not retryable", out)
	}
	out, refused = callMCP(t, client, "agenthail_answer", map[string]any{"requestId": "ask-elsewhere", "sessionId": "laptop", "decision": "allow"})
	if !refused || out["code"] != "invalid" || out["retryable"] == true || !strings.Contains(out["message"].(string), "ambiguous") {
		t.Fatalf("an answer naming a session by a name two sessions on the paired host match: %v, want it refused as invalid", out)
	}
	out, refused = callMCP(t, client, "agenthail_answer", map[string]any{"requestId": "ask-elsewhere", "sessionId": "nobody-here", "decision": "allow"})
	if !refused || out["code"] != "not_found" || out["message"] != "No open request ask-elsewhere." {
		t.Fatalf("an answer naming a session no host has: %v, want not_found for the request", out)
	}
}

func TestAConflictFromAPairedHostWithoutCandidatesStaysRetryable(t *testing.T) {
	l := leadOnTheOtherHost(t)
	l.laptop.gate.oldResolve.Store(true)
	lead, err := l.vps.reg.Session("vps-a")
	if err != nil {
		t.Fatal(err)
	}
	client := mcpSession(t, mcpServer(t, l.vps.daemon), issueMCPToken(t, l.vps.daemon, *lead))
	out, refused := callMCP(t, client, "agenthail_send", map[string]any{"target": "laptop", "message": "hello", "mode": "queue"})
	if !refused || out["code"] == "invalid" || out["retryable"] != true {
		t.Fatalf("a 409 ambiguous_target without candidates, as an older host answers any failure: %v, want it refused as retryable", out)
	}
	out, refused = callMCP(t, client, "agenthail_answer", map[string]any{"requestId": "ask-elsewhere", "sessionId": "laptop", "decision": "allow"})
	if !refused || out["code"] != "refused" || out["retryable"] != true || !strings.Contains(out["message"].(string), "laptop-a, laptop-gadget") {
		t.Fatalf("an answer naming a session an older host answers 409 without candidates for: %v, want it refused as retryable with that host's text", out)
	}
}

func TestResolveAnswersConflictOnlyForANameSeveralSessionsMatch(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	for _, id := range []string{"probe-one", "probe-two"} {
		if err := r.RecordObservedSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "shared probe " + id, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
	}
	handler, token := dashboardRouter(d), pairedControlToken(t, r)
	resolve := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/resolve?target="+target, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := resolve("shared"); response.Code != http.StatusConflict || !ambiguousTarget(response) || !strings.Contains(response.Body.String(), `"candidates":["probe-one","probe-two"]`) {
		t.Fatalf("a name two sessions match: status=%d body=%s, want 409 ambiguous_target naming both candidates", response.Code, response.Body)
	}
	if _, err := r.WriteDB().Exec(`DROP TABLE session_archive`); err != nil {
		t.Fatal(err)
	}
	if response := resolve("probe-one"); response.Code == http.StatusConflict || ambiguousTarget(response) {
		t.Fatalf("a registry failure on a name one session matches: status=%d body=%s, want it not reported as an ambiguous name", response.Code, response.Body)
	}
}

func ambiguousTarget(response *httptest.ResponseRecorder) bool {
	var body map[string]any
	return json.Unmarshal(response.Body.Bytes(), &body) == nil && errorCode(body) == "ambiguous_target"
}
