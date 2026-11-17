package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestOnlyACoordinatorsPromptCarriesItsBacklogLine(t *testing.T) {
	withoutSessionContextLimit(t)
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	cwd := t.TempDir()
	for _, id := range []string{"lead", "worker"} {
		if err := store.RecordObservedSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: cwd, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SetCoordinator(registry.CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: cwd, Name: "widget-lead", Surface: "claude", Cwd: cwd}, registry.CoordinatorChange{SessionID: "lead", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ledger/backlog-line" {
			http.NotFound(w, r)
			return
		}
		asked.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"line": "Backlog: w-1 ready (later: its revisit date came)."})
	}))
	t.Cleanup(server.Close)
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}
	contextOf := func(sessionID string) string {
		t.Helper()
		app.sessionContext(strings.NewReader(`{"session_id":"`+sessionID+`","hook_event_name":"SessionStart","source":"startup"}`), surface.KindClaude, 4242, time.Now())
		output := app.sessionContext(strings.NewReader(`{"session_id":"`+sessionID+`","hook_event_name":"UserPromptSubmit","prompt":"go","prompt_id":"p-`+sessionID+`"}`), surface.KindClaude, 4242, time.Now())
		if output == nil {
			return ""
		}
		return output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	}
	if text := contextOf("worker"); strings.Contains(text, "Backlog:") || asked.Load() != 0 {
		t.Fatalf("a worker's prompt asked for the backlog %d times and carried %q", asked.Load(), text)
	}
	if text := contextOf("lead"); !strings.Contains(text, "Backlog: w-1 ready") {
		t.Fatalf("the coordinator's prompt carried %q", text)
	}
}
