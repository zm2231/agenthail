package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

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
		SessionID: "s1", MessageID: "u-abc123", Prompt: "build the widget",
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
		SessionID: "s2", MessageID: "u-def456", Prompt: "should not capture",
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
		SessionID: "coord-1", MessageID: "u-coord1", Prompt: "coordinator capture",
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
		SessionID: "worker-1", MessageID: "u-worker1", Prompt: "not a coordinator",
	})
	if resp.Captured {
		t.Fatalf("expected non-coordinator NOT captured in unset project, got %+v", resp)
	}
}

func TestLedgerAPIDedupsByMessageID(t *testing.T) {
	_, r, handler := ledgerDaemon(t)

	projectDir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{
		ID: "proj-5", WorkspaceRoot: projectDir, Title: "Dedup", Key: "dedup-key",
	}); err != nil {
		t.Fatal(err)
	}
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable("dedup-key"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{
		ID: "s5", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	resp1 := ledgerPost(t, handler, ledgerCaptureRequest{
		SessionID: "s5", MessageID: "u-same-id", Prompt: "first capture",
	})
	if !resp1.Captured {
		t.Fatal("first capture should succeed")
	}

	resp2 := ledgerPost(t, handler, ledgerCaptureRequest{
		SessionID: "s5", MessageID: "u-same-id", Prompt: "first capture",
	})
	if !resp2.Captured {
		t.Fatal("second capture should report captured (idempotent)")
	}

	events, err := store.Events("dedup-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after dedup, got %d", len(events))
	}
}
