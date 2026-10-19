package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestMobileClaudeCreationOpensTheSessionInTheHostAndRegistersIt(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	home := host.Cwd
	d := New(registry, []surface.Surface{surfaces.NewClaude("", host.Home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-options", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"claude"`) {
		t.Fatal(response.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "--literal instruction", "cwd": home, "model": "sonnet", "name": "phone-task", "worktree": "phone-work", "agent": "reviewer", "effort": "high", "permissionMode": "plan"})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var receipt struct {
		OK      bool                `json:"ok"`
		Session surface.Session     `json:"session"`
		Result  *surface.SendResult `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(response.Body.String(), err)
	}
	if response.Code != 201 || !receipt.OK || receipt.Session.RuntimeKind != surface.RuntimeClaudeHost || receipt.Result == nil || !receipt.Result.Accepted {
		t.Fatal(response.Body.String())
	}
	if stored, err := registry.Session(receipt.Session.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.Runtime == nil || stored.Runtime.Launcher != surface.LauncherClaudeSDK {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	spawns := host.Spawns(t, receipt.Session.ID)
	if len(spawns) != 1 {
		t.Fatalf("spawns=%+v", spawns)
	}
	argv := strings.Join(spawns[0].Argv, " ")
	for _, want := range []string{"sonnet", "phone-task", "phone-work", "reviewer", "high", "plan"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("missing %q in %s", want, argv)
		}
	}
	claudehosttest.WaitFor(t, "the first turn", func() bool {
		turns := host.Turns(t, receipt.Session.ID)
		return len(turns) == 1 && turns[0].Text == "--literal instruction" && turns[0].UUID == receipt.Result.UUID
	})
}

func TestMobileQueuePreservesIntegratedTurnSettings(t *testing.T) {
	d, registry, _, from, _ := daemonFixture(t)
	_, err := registry.QueueMessageWithOptions(from.ID, "queued", "", surface.SendOptions{TurnOptions: surface.TurnOptions{Effort: "high", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	d.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	for _, want := range []string{`"effort":"high"`, `"mode":"plan"`, `"serviceTier":"fast"`, `"outputSchema":{"type":"object"}`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatal(response.Body.String())
		}
	}
}
