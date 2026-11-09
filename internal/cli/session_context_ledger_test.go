package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

type captureCall struct {
	SessionID   string `json:"sessionId"`
	PromptID    string `json:"promptId"`
	Prompt      string `json:"prompt"`
	MessageUUID string `json:"messageUuid"`
}

func ledgerHookFixture(t *testing.T, captured, attach bool) (*App, func() []captureCall) {
	t.Helper()
	withoutSessionContextLimit(t)
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	if err := store.RegisterSession(surface.Session{
		ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: t.TempDir(), Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []captureCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ledger/capture" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var call captureCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"captured": captured, "attach": attach, "projectKey": "test-key", "ref": "claude-prompt:" + call.PromptID, "provisional": 3})
	}))
	t.Cleanup(server.Close)
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}
	app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"SessionStart","source":"startup"}`), surface.KindClaude, 4242, time.Now())
	return app, func() []captureCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]captureCall(nil), calls...)
	}
}

func TestTerminalPromptSendsClaudesPromptIDToTheDaemon(t *testing.T) {
	app, calls := ledgerHookFixture(t, true, true)
	output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"yes","prompt_id":"bb6af0fd"}`), surface.KindClaude, 4242, time.Now())
	got := calls()
	if len(got) != 1 || got[0] != (captureCall{SessionID: "s1", PromptID: "bb6af0fd", Prompt: "yes"}) {
		t.Fatalf("capture call %+v", got)
	}
	app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"go","prompt_id":"c0ffee","agenthail_message_uuid":"6f1c2c1e-0000-4000-8000-000000000001"}`), surface.KindClaude, 4242, time.Now())
	if got := calls(); len(got) != 2 || got[1].MessageUUID != "6f1c2c1e-0000-4000-8000-000000000001" {
		t.Fatalf("the host's message uuid did not reach the daemon: %+v", got)
	}
	if output == nil {
		t.Fatal("expected hook output naming the capture")
	}
	text := output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if want := ledger.ProvisionalNote("test-key", 3); !strings.Contains(text, want) || strings.Contains(text, "ledger attach") {
		t.Fatalf("hook context %q should carry only the count %q", text, want)
	}
}

func TestTerminalPromptWithoutAPromptIDIsLeftToTheJournal(t *testing.T) {
	app, calls := ledgerHookFixture(t, true, true)
	app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"yes"}`), surface.KindClaude, 4242, time.Now())
	if got := calls(); len(got) != 0 {
		t.Fatalf("hook captured a prompt it cannot identify: %+v", got)
	}
}

func TestTerminalPromptAddsNoLedgerContextWhenNotCaptured(t *testing.T) {
	app, calls := ledgerHookFixture(t, false, false)
	output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hello","prompt_id":"p-hello"}`), surface.KindClaude, 4242, time.Now())
	if len(calls()) != 1 {
		t.Fatalf("want 1 capture call, got %d", len(calls()))
	}
	if output != nil && strings.Contains(output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string), "intent ledger") {
		t.Fatalf("uncaptured prompt reported as captured: %v", output)
	}
}

func TestTerminalPromptAddsNoLedgerContextWhenOnlyCaptured(t *testing.T) {
	app, calls := ledgerHookFixture(t, true, false)
	output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hello","prompt_id":"p-hello"}`), surface.KindClaude, 4242, time.Now())
	if len(calls()) != 1 {
		t.Fatalf("want 1 capture call, got %d", len(calls()))
	}
	if output != nil && strings.Contains(output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string), "intent ledger") {
		t.Fatalf("a session whose words are only captured was asked to attach: %v", output)
	}
}
