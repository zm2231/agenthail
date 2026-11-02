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
	fake := &daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	return d, r, d.dashboardHandler(&dashboardServer{token: "test-token"})
}

func ledgerPostStatus(t *testing.T, handler http.Handler, req ledgerCaptureRequest) (int, ledgerCaptureResponse) {
	t.Helper()
	body, _ := json.Marshal(req)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ledger/capture", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var resp ledgerCaptureResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, resp
}

func ledgerPost(t *testing.T, handler http.Handler, req ledgerCaptureRequest) ledgerCaptureResponse {
	t.Helper()
	code, resp := ledgerPostStatus(t, handler, req)
	if code != http.StatusOK {
		t.Fatalf("capture status=%d", code)
	}
	return resp
}

type ledgerProject struct {
	dir   string
	key   string
	store *ledger.Store
}

func addLedgerProject(t *testing.T, r *registry.Registry, id, key string) ledgerProject {
	t.Helper()
	dir := t.TempDir()
	if err := r.AddProject(registry.ProjectRecord{ID: id, WorkspaceRoot: dir, Title: id, Key: key}); err != nil {
		t.Fatal(err)
	}
	return ledgerProject{dir: dir, key: key, store: ledger.NewStore(r.WriteDB(), r.ReadDB())}
}

func (p ledgerProject) session(t *testing.T, r *registry.Registry, id string) surface.Session {
	t.Helper()
	s := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: p.dir, Status: surface.StatusIdle}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (p ledgerProject) events(t *testing.T) []ledger.Event {
	t.Helper()
	events, err := p.store.Events(p.key, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func makeCoordinator(t *testing.T, r *registry.Registry, projectID string, p ledgerProject, sessionID string) {
	t.Helper()
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: projectID, ProjectRoot: p.dir, Name: "coord-" + projectID, Surface: "claude", Cwd: p.dir}, registry.CoordinatorChange{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerAPICapturesForEnabledProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-1", "test-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s1")
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-1", Prompt: "build the widget"})
	if !resp.Captured || resp.ProjectKey != "test-key" || resp.Ref != ledger.PromptRef("p-1") {
		t.Fatalf("expected capture of p-1 in test-key, got %+v", resp)
	}
	if events := p.events(t); len(events) != 1 || events[0].Ref != ledger.PromptRef("p-1") {
		t.Fatalf("want one utterance keyed by the prompt id, got %+v", events)
	}
}

func TestLedgerAPISkipsDisabledProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-2", "disabled-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	if err := p.store.Disable(p.key); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s2")
	if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s2", PromptID: "p-2", Prompt: "should not capture"}); resp.Captured {
		t.Fatalf("captured in a disabled project: %+v", resp)
	}
}

func TestLedgerAPICapturesOnlyTheCoordinatorInUnsetProject(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-3", "unset-key")
	p.session(t, r, "coord-1")
	p.session(t, r, "worker-1")
	makeCoordinator(t, r, "proj-3", p, "coord-1")
	if !ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "coord-1", PromptID: "p-3", Prompt: "coordinator capture"}).Captured {
		t.Fatal("coordinator prompt not captured in an unset project")
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "worker-1", PromptID: "p-4", Prompt: "not a coordinator"}).Captured {
		t.Fatal("non-coordinator prompt captured in an unset project")
	}
}

func TestLedgerAPIKeepsRepeatsAndCollapsesHookCopies(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-5", "repeat-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s5")
	for _, req := range []ledgerCaptureRequest{
		{SessionID: "s5", PromptID: "first", Prompt: "yes"},
		{SessionID: "s5", PromptID: "first", Prompt: "yes"},
		{SessionID: "s5", PromptID: "second", Prompt: "yes"},
	} {
		if !ledgerPost(t, handler, req).Captured {
			t.Fatalf("capture %+v not recorded", req)
		}
	}
	if events := p.events(t); len(events) != 2 {
		t.Fatalf("want 2 utterances (one per prompt id), got %d", len(events))
	}
}

func TestLedgerAPIRequiresAPromptID(t *testing.T) {
	_, _, handler := ledgerDaemon(t)
	if code, _ := ledgerPostStatus(t, handler, ledgerCaptureRequest{SessionID: "s", Prompt: "no id"}); code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", code)
	}
}

func TestLedgerAPIIgnoresPeerMessagesDeliveredAsPrompts(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-6", "peer-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	p.session(t, r, "s6")
	for i, prompt := range []string{
		"<cross-session-message from=\"uds:/tmp/x.sock\" from-session=\"peer\" from-name=\"@lead\">\ndo the thing\n</cross-session-message>",
		"<task-notification>\n<task-id>x</task-id>\n</task-notification>",
	} {
		if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s6", PromptID: string(rune('a' + i)), Prompt: prompt}).Captured {
			t.Fatalf("injected prompt %d captured as the person's words", i)
		}
	}
	if events := p.events(t); len(events) != 0 {
		t.Fatalf("want no utterances, got %d", len(events))
	}
}
