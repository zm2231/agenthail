package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestResolveTellsAPairedHostWhichMatchesTheTargetIdentifies(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	if err := r.SetAlias("chief", "to"); err != nil {
		t.Fatal(err)
	}
	claude := surface.Session{ID: "claude-live", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "voice hotfix", Cwd: "/work/voice", PID: 4242, Transcript: "/home/fixture/.claude/projects/work/0d1e2f3a-conversation.jsonl", Status: surface.StatusIdle}
	child := surface.Session{ID: "child-ada", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "subAgent", Transport: "desktop", Subagent: &surface.Subagent{ParentID: "to", RootID: "to", Depth: 1, Nickname: "Ada"}}
	for _, session := range []surface.Session{claude, child} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	fake.sessions["4242"] = claude
	handler, token := dashboardRouter(d), pairedControlToken(t, r)
	for target, exact := range map[string]bool{"@chief": true, "chief": true, "to": true, "claude-li": true, "4242": true, "0d1e2f3a-conversation": true, "@chief/Ada": true, "hotfix": false} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/resolve?target="+url.QueryEscape(target), nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var answer resolveResponse
		if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || len(answer.Matches) != 1 || answer.Matches[0].Exact != exact {
			t.Fatalf("target=%s status=%d body=%s, want one match with exact=%v", target, response.Code, response.Body.String(), exact)
		}
	}
}
