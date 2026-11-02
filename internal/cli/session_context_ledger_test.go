package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func ledgerHookFixture(t *testing.T) (*App, *registry.Registry, *ledger.Store) {
	t.Helper()
	withoutSessionContextLimit(t)
	fake := &cliSurface{kind: surface.KindClaude}
	app, store := cliFixture(t, fake)

	captureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ledger/capture" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			SessionID string `json:"sessionId"`
			MessageID string `json:"messageId"`
			Prompt    string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session, err := store.Session(req.SessionID)
		if err != nil || session.Cwd == "" {
			json.NewEncoder(w).Encode(map[string]any{"captured": false})
			return
		}
		projects, err := store.ListProjects()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"captured": false})
			return
		}
		var projectKey string
		for _, p := range projects {
			if p.Key != "" && strings.HasPrefix(session.Cwd, p.WorkspaceRoot) {
				projectKey = p.Key
				break
			}
		}
		if projectKey == "" {
			json.NewEncoder(w).Encode(map[string]any{"captured": false})
			return
		}
		ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
		enabled, err := ls.Enabled(projectKey)
		if err != nil || !enabled {
			json.NewEncoder(w).Encode(map[string]any{"captured": false})
			return
		}
		ls.RecordUtterance(projectKey, req.MessageID, surface.PersonSessionID, req.SessionID, "terminal", req.Prompt)
		json.NewEncoder(w).Encode(map[string]any{"captured": true, "projectKey": projectKey})
	}))
	t.Cleanup(captureServer.Close)

	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: captureServer.URL, token: "", httpClient: captureServer.Client()}, nil
	}

	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	return app, store, ls
}

func TestTerminalPromptCapturesUtteranceWhenLedgerEnabled(t *testing.T) {
	app, store, ls := ledgerHookFixture(t)

	projectDir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{
		ID: "proj-1", WorkspaceRoot: projectDir, Title: "Test", Key: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ls.Enable("test-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(surface.Session{
		ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	start := `{"session_id":"s1","hook_event_name":"SessionStart","source":"startup"}`
	app.sessionContext(strings.NewReader(start), surface.KindClaude, 4242, time.Now())

	prompt := `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"build the widget"}`
	output := app.sessionContext(strings.NewReader(prompt), surface.KindClaude, 4242, time.Now())
	if output == nil {
		t.Fatal("expected hook output with ledger context")
	}
	ctx := output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
	if !strings.Contains(ctx, "intent ledger") || !strings.Contains(ctx, "captured") {
		t.Fatalf("expected ledger capture context, got %q", ctx)
	}

	events, err := ls.Events("test-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 utterance event, got %d", len(events))
	}
	if events[0].Type != ledger.EventUtterance {
		t.Fatalf("expected utterance, got %s", events[0].Type)
	}
}

func TestTerminalPromptSkipsCaptureWhenLedgerDisabled(t *testing.T) {
	app, store, ls := ledgerHookFixture(t)

	projectDir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{
		ID: "proj-2", WorkspaceRoot: projectDir, Title: "Disabled", Key: "disabled-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ls.Enable("disabled-key"); err != nil {
		t.Fatal(err)
	}
	if err := ls.Disable("disabled-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(surface.Session{
		ID: "s2", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	app.sessionContext(strings.NewReader(`{"session_id":"s2","hook_event_name":"SessionStart","source":"startup"}`), surface.KindClaude, 4242, time.Now())
	app.sessionContext(strings.NewReader(`{"session_id":"s2","hook_event_name":"UserPromptSubmit","prompt":"should not capture"}`), surface.KindClaude, 4242, time.Now())

	events, err := ls.Events("disabled-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for disabled project, got %d", len(events))
	}
}

func TestTerminalPromptDedupsByMessageID(t *testing.T) {
	app, store, ls := ledgerHookFixture(t)

	projectDir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{
		ID: "proj-3", WorkspaceRoot: projectDir, Title: "Dedup", Key: "dedup-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := ls.Enable("dedup-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(surface.Session{
		ID: "s3", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	app.sessionContext(strings.NewReader(`{"session_id":"s3","hook_event_name":"SessionStart","source":"startup"}`), surface.KindClaude, 4242, time.Now())

	prompt := `{"session_id":"s3","hook_event_name":"UserPromptSubmit","prompt":"same prompt twice"}`
	app.sessionContext(strings.NewReader(prompt), surface.KindClaude, 4242, time.Now())
	app.sessionContext(strings.NewReader(prompt), surface.KindClaude, 4242, time.Now())

	events, err := ls.Events("dedup-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after dedup (same session+prompt = same messageId), got %d", len(events))
	}
}
