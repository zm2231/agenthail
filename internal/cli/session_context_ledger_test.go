package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type captureCall struct {
	SessionID     string `json:"sessionId"`
	CaptureRef    string `json:"captureRef"`
	Prompt        string `json:"prompt"`
	SubmittedAtMs int64  `json:"submittedAtMs"`
}

func ledgerHookFixture(t *testing.T, captured bool) (*App, func() []captureCall) {
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
		json.NewEncoder(w).Encode(map[string]any{"captured": captured, "projectKey": "test-key"})
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

func TestTerminalPromptSendsEachSubmissionToTheDaemon(t *testing.T) {
	app, calls := ledgerHookFixture(t, true)
	prompt := `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"yes"}`
	first := time.UnixMilli(1_800_000_000_000)
	output := app.sessionContext(strings.NewReader(prompt), surface.KindClaude, 4242, first)
	app.sessionContext(strings.NewReader(prompt), surface.KindClaude, 4242, first.Add(time.Minute))

	got := calls()
	if len(got) != 2 {
		t.Fatalf("want 2 capture calls, got %+v", got)
	}
	if got[0].CaptureRef == "" || got[0].CaptureRef == got[1].CaptureRef {
		t.Fatalf("each submission needs its own capture ref: %+v", got)
	}
	if got[0].SessionID != "s1" || got[0].Prompt != "yes" || got[0].SubmittedAtMs != first.UnixMilli() || got[1].SubmittedAtMs != first.Add(time.Minute).UnixMilli() {
		t.Fatalf("capture calls carry the wrong session, prompt or submit time: %+v", got)
	}
	if output == nil {
		t.Fatal("expected hook output naming the capture")
	}
	if text := output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string); !strings.Contains(text, got[0].CaptureRef) {
		t.Fatalf("hook context %q does not name capture %s", text, got[0].CaptureRef)
	}
}

func TestTerminalPromptAddsNoLedgerContextWhenNotCaptured(t *testing.T) {
	app, calls := ledgerHookFixture(t, false)
	output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hello"}`), surface.KindClaude, 4242, time.Now())
	if len(calls()) != 1 {
		t.Fatalf("want 1 capture call, got %d", len(calls()))
	}
	if output != nil && strings.Contains(output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string), "intent ledger") {
		t.Fatalf("uncaptured prompt reported as captured: %v", output)
	}
}
