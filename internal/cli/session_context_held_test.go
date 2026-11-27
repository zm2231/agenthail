package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func heldWorkFixture(t *testing.T) *App {
	t.Helper()
	withoutSessionContextLimit(t)
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	if err := store.RecordObservedSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir(), Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ledgerCheckpointRequestBody
		if r.URL.Path != "/api/v1/ledger/checkpoint" || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.NotFound(w, r)
			return
		}
		reply := map[string]any{}
		if req.Start {
			reply["held"] = []ledger.WorkItem{{ID: "widget-W7", Text: "tidy the notes", State: ledger.WorkInProgress}}
		}
		json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(server.Close)
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}
	return app
}

func TestACompactedSessionIsRemindedToLogToTheWorkItHolds(t *testing.T) {
	app := heldWorkFixture(t)
	output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"SessionStart","source":"compact"}`), surface.KindClaude, 4242, time.Now())
	if output == nil {
		t.Fatal("no start context")
	}
	text := output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if !strings.Contains(text, "You hold widget-W7 (tidy the notes).") || !strings.Contains(text, ledger.HolderGuidance("widget-W7", false)) {
		t.Fatalf("start context %q does not name the held item and how to log to it", text)
	}
}

func TestASessionNoStartHookReachedIsToldTheWorkItHoldsWithItsFirstPromptOnly(t *testing.T) {
	app := heldWorkFixture(t)
	told := func(prompt string) string {
		output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"`+prompt+`"}`), surface.KindClaude, 4242, time.Now())
		if output == nil {
			return ""
		}
		return output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	}
	if first := told("first"); !strings.HasPrefix(first, "[Agenthail] You are session s1 (") || !strings.Contains(first, "You hold widget-W7 (tidy the notes).") {
		t.Fatalf("the first prompt was told %q, want its card and the work it holds", first)
	}
	if second := told("second"); strings.Contains(second, "widget-W7") {
		t.Fatalf("the second prompt was told %q again", second)
	}
}

type ledgerCheckpointRequestBody struct {
	Start bool `json:"start"`
}
