package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnOptedInSessionOutsideTheFolderIsCapturedFromItsGrant(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-opt", "opt-key")
	outside := surface.Session{ID: "outside", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir(), Status: surface.StatusIdle}
	if err := r.RegisterSession(outside); err != nil {
		t.Fatal(err)
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "outside", PromptID: "before", Prompt: "not yet in scope"}).Captured {
		t.Fatal("captured a session that was never opted in")
	}
	history := time.Now().Add(-time.Hour)
	if status := ledgerScopePost(t, handler, ledgerScopeRequest{ProjectKey: p.key, Enabled: true, SessionID: "outside"}); status != http.StatusOK {
		t.Fatalf("grant answered %d", status)
	}
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "outside", PromptID: "after", Prompt: "now opted in"})
	if !resp.Captured || resp.ProjectKey != p.key {
		t.Fatalf("opted-in session not captured into %s: %+v", p.key, resp)
	}
	journalSource(d, outside, nil).appendSeedItems([]surface.TimelineItem{typedPrompt("old", "old", "from before the grant", history)})
	if events := p.events(t); len(events) != 1 || events[0].Ref != ledger.PromptRef("after") {
		t.Fatalf("want only the prompt after the grant, got %+v", events)
	}

	if status := ledgerScopePost(t, handler, ledgerScopeRequest{ProjectKey: p.key, SessionID: "outside"}); status != http.StatusOK {
		t.Fatalf("revoke answered %d", status)
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "outside", PromptID: "revoked", Prompt: "after revoke"}).Captured {
		t.Fatal("captured after the grant was revoked")
	}
	ledgerScopePost(t, handler, ledgerScopeRequest{ProjectKey: p.key, Enabled: true, SessionID: "outside"})
	if status := ledgerScopePost(t, handler, ledgerScopeRequest{ProjectKey: p.key}); status != http.StatusOK {
		t.Fatalf("disable answered %d", status)
	}
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "outside", PromptID: "disabled", Prompt: "project disabled"}).Captured {
		t.Fatal("a grant overrode a disabled project")
	}
}

func TestAGrantAddsAProjectWithoutCapturingTheFolderProjectOutOfScope(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	home := addLedgerProject(t, r, "proj-home", "home-key")
	other := addLedgerProject(t, r, "proj-other", "other-key")
	home.session(t, r, "worker")
	if err := other.store.GrantSession(other.key, "worker"); err != nil {
		t.Fatal(err)
	}
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "worker", PromptID: "p", Prompt: "for the other project"})
	if !resp.Captured || resp.ProjectKey != other.key {
		t.Fatalf("want capture into %s only, got %+v", other.key, resp)
	}
	if events := home.events(t); len(events) != 0 {
		t.Fatalf("the unset folder project captured a non-coordinator: %+v", events)
	}
	if err := home.store.Enable(home.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "worker", PromptID: "both", Prompt: "for both"})
	if len(home.events(t)) != 1 || len(other.events(t)) != 2 {
		t.Fatalf("a session in scope of two projects must be recorded in both: home %d other %d", len(home.events(t)), len(other.events(t)))
	}
}

func ledgerScopePost(t *testing.T, handler http.Handler, req ledgerScopeRequest) int {
	t.Helper()
	body, _ := json.Marshal(req)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ledger/scope", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	return w.Code
}

func TestTheScopeRouteSetsTheAttachModeAndRefusesWhatItCannotApply(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-scope", "scope-key")
	p.session(t, r, "worker")
	if status := ledgerScopePost(t, handler, ledgerScopeRequest{ProjectKey: p.key, Enabled: true, Attach: ledger.AttachAtCheckpoints}); status != http.StatusOK {
		t.Fatalf("enable answered %d", status)
	}
	if scope, err := p.store.Scope(p.key); err != nil || scope.State != ledger.LedgerEnabled || scope.Attach != ledger.AttachAtCheckpoints {
		t.Fatalf("scope after enable %+v %v", scope, err)
	}
	refused := map[string]struct {
		req  ledgerScopeRequest
		want int
	}{
		"unknown mode":      {ledgerScopeRequest{ProjectKey: p.key, Enabled: true, Attach: "whenever"}, http.StatusBadRequest},
		"mode on a session": {ledgerScopeRequest{ProjectKey: p.key, Enabled: true, SessionID: "worker", Attach: ledger.AttachEachPrompt}, http.StatusBadRequest},
		"mode on disable":   {ledgerScopeRequest{ProjectKey: p.key, Attach: ledger.AttachEachPrompt}, http.StatusBadRequest},
		"unknown session":   {ledgerScopeRequest{ProjectKey: p.key, Enabled: true, SessionID: "nobody"}, http.StatusNotFound},
		"unknown project":   {ledgerScopeRequest{ProjectKey: "no-such-key", Enabled: true}, http.StatusNotFound},
	}
	for name, c := range refused {
		if status := ledgerScopePost(t, handler, c.req); status != c.want {
			t.Errorf("%s answered %d, want %d", name, status, c.want)
		}
	}
	if scope, _ := p.store.Scope(p.key); scope.State != ledger.LedgerEnabled || scope.Attach != ledger.AttachAtCheckpoints {
		t.Fatalf("a refused request changed the scope: %+v", scope)
	}
	if grants, _ := p.store.SessionGrants("nobody"); len(grants) != 0 {
		t.Fatalf("granted an unknown session: %+v", grants)
	}
}
