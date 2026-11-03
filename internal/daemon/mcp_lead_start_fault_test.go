package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const failOriginTrigger = `CREATE TRIGGER fail_origin BEFORE INSERT ON session_origins BEGIN SELECT RAISE(ABORT, 'injected'); END`

func refusedAsRevoked(t *testing.T, server *httptest.Server, token string) bool {
	t.Helper()
	status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token})
	return status == http.StatusUnauthorized && errorCode(body) == "credential_revoked"
}

func TestAClaudeCoordinatorThatFailsToBecomeTheLeadLosesItsLeadCredential(t *testing.T) {
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-new","surface":"claude","cwd":%q,"name":"captain"}`, f.host.Cwd))
	if _, err := f.registry.WriteDB().Exec(failOriginTrigger); err != nil {
		t.Fatal(err)
	}
	w := serveDashboardRequest(dashboardRouter(f.daemon), http.MethodPost, "/api/action", `{"action":"coordinator-send","name":"captain","message":"hello"}`)
	if w.Code < 300 {
		t.Fatalf("coordinator-send succeeded with origin writes failing: status=%d", w.Code)
	}
	sessions, err := f.registry.ListSessions(0)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%d err=%v", len(sessions), err)
	}
	id := sessions[0].ID
	spawns := f.host.Spawns(t, id)
	if len(spawns) != 1 {
		t.Fatalf("spawns=%d", len(spawns))
	}
	if !refusedAsRevoked(t, f.server, spawnBearer(t, spawns[0])) {
		t.Fatal("the first credential still works although the session never became the lead")
	}
	replaced := f.replacedTokens(t, id)
	if len(replaced) != 1 || slices.Contains(f.capabilities(t, replaced[0]), any("control.project")) {
		t.Fatalf("replacements=%d, the session kept project control", len(replaced))
	}
}

func newCodexCaptain(t *testing.T) *mcpCodexFixture {
	t.Helper()
	fixture := newMCPCodexFixture(t)
	cwd := t.TempDir()
	root, err := canonicalCatalogPath(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.AddProject(registry.ProjectRecord{ID: "p-codex", WorkspaceRoot: root, Title: "Codex", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if code, body := fixture.action(t, map[string]any{"action": "coordinator-new", "surface": "codex", "cwd": cwd, "name": "captain"}); code != http.StatusOK {
		t.Fatalf("coordinator-new status=%d body=%v", code, body)
	}
	return fixture
}

func (f *mcpCodexFixture) firstBearer(t *testing.T) string {
	t.Helper()
	starts := f.lifecycle.Daemon().Calls("thread/start")
	if len(starts) != 1 {
		t.Fatalf("thread/start calls=%d", len(starts))
	}
	return codexBearer(starts[0])
}

func TestAFailedCoordinatorCorrectionIsStillMadeAfterAgenthailRestarts(t *testing.T) {
	fixture := newCodexCaptain(t)
	for _, trigger := range []string{failOriginTrigger, `CREATE TRIGGER fail_rotation BEFORE UPDATE OF revoked_at_ms ON session_credentials BEGIN SELECT RAISE(ABORT, 'injected'); END`} {
		if _, err := fixture.registry.WriteDB().Exec(trigger); err != nil {
			t.Fatal(err)
		}
	}
	fixture.action(t, map[string]any{"action": "coordinator-send", "name": "captain", "message": "hello"})
	first := fixture.firstBearer(t)
	fixture.restartAgenthail(t)
	if _, err := fixture.registry.WriteDB().Exec(`DROP TRIGGER fail_rotation`); err != nil {
		t.Fatal(err)
	}
	fixture.daemon.syncLeads()
	if !refusedAsRevoked(t, fixture.server, first) {
		t.Fatal("the correction a restart interrupted was never made")
	}
}

func TestALeadThatStepsDownWhileAgenthailIsStoppedLosesProjectControlOnRestart(t *testing.T) {
	fixture := newCodexCaptain(t)
	if code, body := fixture.action(t, map[string]any{"action": "coordinator-send", "name": "captain", "message": "hello"}); code != http.StatusOK {
		t.Fatalf("coordinator-send status=%d body=%s", code, redacted(body))
	}
	first := fixture.firstBearer(t)
	if _, err := fixture.registry.ClearCoordinator("p-codex", "test", time.Time{}); err != nil {
		t.Fatal(err)
	}
	fixture.restartAgenthail(t)
	fixture.daemon.syncLeads()
	if !refusedAsRevoked(t, fixture.server, first) {
		t.Fatal("a lead credential outlived its lead across a restart")
	}
}

func TestACredentialWhoseCapabilitiesAreWrongWhenAgenthailStartsIsFixedWithinOneTick(t *testing.T) {
	fixture := newMCPCodexFixture(t)
	code, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir()})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if code >= 300 || id == "" {
		t.Fatalf("create status=%d", code)
	}
	wrong, err := fixture.registry.RotateSessionCredential(id, sessionCapabilities(true), json.RawMessage(`{}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fixture.restartAgenthail(t)
	fixture.daemon.syncLeads()
	if !refusedAsRevoked(t, fixture.server, wrong.Token) {
		t.Fatal("a credential granting project control to a non-lead survived a tick")
	}
	held, err := fixture.registry.LiveCredentials()
	if err != nil || !sameCapabilities(held[id].Capabilities, sessionCapabilities(false)) {
		t.Fatalf("live capabilities=%v err=%v", held[id].Capabilities, err)
	}
}

func TestACodexCoordinatorThatFailsToBecomeTheLeadLosesItsLeadCredential(t *testing.T) {
	fixture := newCodexCaptain(t)
	if _, err := fixture.registry.WriteDB().Exec(failOriginTrigger); err != nil {
		t.Fatal(err)
	}
	if code, _ := fixture.action(t, map[string]any{"action": "coordinator-send", "name": "captain", "message": "hello"}); code == http.StatusOK {
		t.Fatal("coordinator-send succeeded with origin writes failing")
	}
	if !refusedAsRevoked(t, fixture.server, fixture.firstBearer(t)) {
		t.Fatal("the first credential still works although the thread never became the lead")
	}
}

func TestAStartedCoordinatorWhoseCorrectionFailsIsRetriedUntilItTakes(t *testing.T) {
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-new","surface":"claude","cwd":%q,"name":"captain"}`, f.host.Cwd))
	if _, err := f.registry.WriteDB().Exec(failOriginTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.WriteDB().Exec(`CREATE TRIGGER fail_rotation BEFORE UPDATE OF revoked_at_ms ON session_credentials BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	serveDashboardRequest(dashboardRouter(f.daemon), http.MethodPost, "/api/action", `{"action":"coordinator-send","name":"captain","message":"hello"}`)
	sessions, err := f.registry.ListSessions(0)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%d err=%v", len(sessions), err)
	}
	id := sessions[0].ID
	first := spawnBearer(t, f.host.Spawns(t, id)[0])
	if !slices.Contains(f.capabilities(t, first), any("control.project")) {
		t.Fatal("the injected rotation failure did not hold the lead credential in place")
	}
	if _, err := f.registry.WriteDB().Exec(`DROP TRIGGER fail_rotation`); err != nil {
		t.Fatal(err)
	}
	f.daemon.syncLeads()
	if !refusedAsRevoked(t, f.server, first) {
		t.Fatal("the failed correction was not retried")
	}
}
