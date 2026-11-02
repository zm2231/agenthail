package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

const submitted = int64(1_800_000_000_000)

func ledgerDaemon(t *testing.T) (*Daemon, *registry.Registry, http.Handler) {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fake := &daemonSurface{
		sessions:     map[string]surface.Session{},
		observations: map[string]*surface.TurnObservation{},
		accepted:     true,
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	handler := d.dashboardHandler(&dashboardServer{token: "test-token"})
	return d, r, handler
}

func ledgerPost(t *testing.T, handler http.Handler, req ledgerCaptureRequest) ledgerCaptureResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ledger/capture", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("capture status=%d body=%s", w.Code, w.Body.String())
	}
	var resp ledgerCaptureResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLedgerAPICapturesForEnabledProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)

	projectDir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{
		ID: "proj-1", WorkspaceRoot: projectDir, Title: "Test", Key: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable("test-key"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{
		ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	resp := ledgerPost(t, handler, ledgerCaptureRequest{
		SessionID: "s1", CaptureRef: "h-1", Prompt: "build the widget", SubmittedAtMs: submitted,
	})
	if !resp.Captured || resp.ProjectKey != "test-key" {
		t.Fatalf("expected captured=true projectKey=test-key, got %+v", resp)
	}

	events, err := store.Events("test-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != ledger.EventUtterance {
		t.Fatalf("expected 1 utterance, got %d events", len(events))
	}
}

func TestLedgerAPISkipsDisabledProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)

	projectDir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{
		ID: "proj-2", WorkspaceRoot: projectDir, Title: "Disabled", Key: "disabled-key",
	}); err != nil {
		t.Fatal(err)
	}
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable("disabled-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.Disable("disabled-key"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{
		ID: "s2", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	resp := ledgerPost(t, handler, ledgerCaptureRequest{
		SessionID: "s2", CaptureRef: "h-2", Prompt: "should not capture", SubmittedAtMs: submitted,
	})
	if resp.Captured {
		t.Fatalf("expected captured=false for disabled project, got %+v", resp)
	}
}

func TestLedgerAPICapturesCoordinatorInUnsetProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)

	projectDir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{
		ID: "proj-3", WorkspaceRoot: projectDir, Title: "Unset", Key: "unset-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{
		ID: "coord-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{
		ProjectID: "proj-3", ProjectRoot: projectDir, Name: "test-coord", Surface: "claude", Cwd: projectDir,
	}, registry.CoordinatorChange{SessionID: "coord-1"}); err != nil {
		t.Fatal(err)
	}

	resp := ledgerPost(t, handler, ledgerCaptureRequest{
		SessionID: "coord-1", CaptureRef: "h-3", Prompt: "coordinator capture", SubmittedAtMs: submitted,
	})
	if !resp.Captured {
		t.Fatalf("expected coordinator captured in unset project, got %+v", resp)
	}
}

func TestLedgerAPISkipsNonCoordinatorInUnsetProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)

	projectDir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{
		ID: "proj-4", WorkspaceRoot: projectDir, Title: "Unset", Key: "unset-key-2",
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{
		ID: "worker-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	resp := ledgerPost(t, handler, ledgerCaptureRequest{
		SessionID: "worker-1", CaptureRef: "h-4", Prompt: "not a coordinator", SubmittedAtMs: submitted,
	})
	if resp.Captured {
		t.Fatalf("expected non-coordinator NOT captured in unset project, got %+v", resp)
	}
}

func enabledLedgerProject(t *testing.T, r *registry.Registry, sessionID string) *ledger.Store {
	t.Helper()
	projectDir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{ID: "proj-5", WorkspaceRoot: projectDir, Title: "Repeats", Key: "repeat-key"}); err != nil {
		t.Fatal(err)
	}
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable("repeat-key"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: sessionID, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: projectDir, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestLedgerAPIKeepsRepeatedPromptsAndAbsorbsRetries(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	store := enabledLedgerProject(t, r, "s5")
	for _, req := range []ledgerCaptureRequest{
		{SessionID: "s5", CaptureRef: "h-a", Prompt: "yes", SubmittedAtMs: submitted},
		{SessionID: "s5", CaptureRef: "h-a", Prompt: "yes", SubmittedAtMs: submitted},
		{SessionID: "s5", CaptureRef: "h-b", Prompt: "yes", SubmittedAtMs: submitted + 60_000},
	} {
		if !ledgerPost(t, handler, req).Captured {
			t.Fatalf("capture %+v not recorded", req)
		}
	}
	events, err := store.Events("repeat-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 utterances (a retry and a real repeat), got %d", len(events))
	}
}

func TestLedgerAPIIgnoresPeerMessagesDeliveredAsPrompts(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	store := enabledLedgerProject(t, r, "s6")
	for i, prompt := range []string{
		"<cross-session-message from=\"uds:/tmp/x.sock\" from-session=\"peer\" from-name=\"@lead\">\ndo the thing\n</cross-session-message>",
		"<task-notification>\n<task-id>x</task-id>\n</task-notification>",
	} {
		if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s6", CaptureRef: fmt.Sprintf("h-%d", i), Prompt: prompt, SubmittedAtMs: submitted}).Captured {
			t.Fatalf("injected prompt %d captured as the person's words", i)
		}
	}
	if events, _ := store.Events("repeat-key", 0, 100); len(events) != 0 {
		t.Fatalf("want no utterances, got %d", len(events))
	}
}
