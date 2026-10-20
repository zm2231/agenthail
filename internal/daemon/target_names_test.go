package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestResolveNamedTargetMatchesOnlyThatName(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	if err := r.SetAlias("chief", "to"); err != nil {
		t.Fatal(err)
	}
	handler, token := dashboardRouter(d), pairedControlToken(t, r)
	if err := r.RegisterSession(surface.Session{ID: "session_named", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "claudeprobe-a3", Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]int{"@probe-a": 0, "@chief": 1} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/resolve?target="+target, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var answer resolveResponse
		if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || len(answer.Matches) != want {
			t.Fatalf("target=%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
}
