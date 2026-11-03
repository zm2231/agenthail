package daemon

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
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

func credentialRows(t *testing.T, r *registry.Registry, sessionID string) int {
	t.Helper()
	var rows int
	if err := r.WriteDB().QueryRow(`SELECT COUNT(*) FROM session_credentials WHERE session_id=?`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f *mcpCodexFixture) controlsProject(t *testing.T, token string) bool {
	t.Helper()
	whoami, refused := f.whoami(t, token)
	if refused {
		t.Fatalf("whoami refused: %v", whoami)
	}
	capabilities, _ := whoami["capabilities"].([]any)
	return slices.Contains(capabilities, any("control.project"))
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

func TestBecomingAndLeavingCoordinatorChangesTheSameTokensNextCall(t *testing.T) {
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	id, token := f.create(t)
	if slices.Contains(f.capabilities(t, token), any("control.project")) {
		t.Fatal("a session that coordinates nothing may control its project")
	}
	f.action(t, fmt.Sprintf(`{"action":"coordinator-set","sessionId":%q,"name":"captain"}`, id))
	if !slices.Contains(f.capabilities(t, token), any("control.project")) {
		t.Fatal("the new lead's next call cannot control its project")
	}
	f.action(t, `{"action":"coordinator-clear","projectId":"p-lead"}`)
	if slices.Contains(f.capabilities(t, token), any("control.project")) {
		t.Fatal("a lead that stepped down still controls its project")
	}
	if rows, swaps := credentialRows(t, f.registry, id), len(f.replacedTokens(t, id)); rows != 1 || swaps != 0 {
		t.Fatalf("credentials=%d swaps=%d, a role change minted or pushed a token", rows, swaps)
	}
}

func TestALoadedCodexThreadThatBecomesLeadControlsItsProjectOnItsNextCall(t *testing.T) {
	fixture := newMCPCodexFixture(t)
	cwd := t.TempDir()
	root, err := canonicalCatalogPath(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.AddProject(registry.ProjectRecord{ID: "p-codex", WorkspaceRoot: root, Title: "Codex", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First", "cwd": cwd})
	id := created["session"].(map[string]any)["id"].(string)
	started := fixture.firstBearer(t)
	if code, body := fixture.action(t, map[string]any{"action": "coordinator-set", "sessionId": id, "name": "captain"}); code != http.StatusOK {
		t.Fatalf("coordinator-set status=%d body=%v", code, body)
	}
	if !fixture.controlsProject(t, started) {
		t.Fatal("the loaded thread's own token cannot control its project after it became the lead")
	}
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Again"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	if unsubscribes := len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe")); unsubscribes != 0 {
		t.Fatalf("the thread was reloaded %d times for a role change", unsubscribes)
	}
	for _, resume := range fixture.lifecycle.Daemon().Calls("thread/resume") {
		if bearer := codexBearer(resume); bearer != started {
			t.Fatalf("a resume carried %s instead of the thread's token", redacted(bearer))
		}
	}
	if rows := credentialRows(t, fixture.registry, id); rows != 1 {
		t.Fatalf("credentials=%d, becoming the lead minted a token", rows)
	}
}

func TestAnArchivedOrRevokedSessionsNextCallIsRefused(t *testing.T) {
	for name, end := range map[string]func(*registry.Registry, string) error{
		"archived": func(r *registry.Registry, id string) error { return r.ArchiveSession(id, time.Now()) },
		"revoked": func(r *registry.Registry, id string) error {
			return r.RevokeSessionCredentials(id, registry.RevokedByPerson, time.Now())
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newMCPClaudeFixture(t)
			id, token := f.create(t)
			f.capabilities(t, token)
			if err := end(f.registry, id); err != nil {
				t.Fatal(err)
			}
			if !refusedAsRevoked(t, f.server, token) {
				t.Fatal("the session's next call was served")
			}
			if _, err := f.daemon.mcpCredentials.Load().Rotate(id); name == "archived" && err == nil {
				t.Fatal("an archived session was given a new token")
			}
		})
	}
}

func TestACoordinatorThatFailsToBecomeTheLeadNeverControlsTheProject(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
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
		spawns := f.host.Spawns(t, sessions[0].ID)
		if len(spawns) != 1 {
			t.Fatalf("spawns=%d", len(spawns))
		}
		if slices.Contains(f.capabilities(t, spawnBearer(t, spawns[0])), any("control.project")) {
			t.Fatal("a session that never became the lead controls its project")
		}
	})
	t.Run("codex", func(t *testing.T) {
		fixture := newCodexCaptain(t)
		if _, err := fixture.registry.WriteDB().Exec(failOriginTrigger); err != nil {
			t.Fatal(err)
		}
		if code, _ := fixture.action(t, map[string]any{"action": "coordinator-send", "name": "captain", "message": "hello"}); code == http.StatusOK {
			t.Fatal("coordinator-send succeeded with origin writes failing")
		}
		if fixture.controlsProject(t, fixture.firstBearer(t)) {
			t.Fatal("a thread that never became the lead controls its project")
		}
	})
}

func TestALeadThatStepsDownWhileAgenthailIsStoppedLosesProjectControlOnRestart(t *testing.T) {
	fixture := newCodexCaptain(t)
	if code, body := fixture.action(t, map[string]any{"action": "coordinator-send", "name": "captain", "message": "hello"}); code != http.StatusOK {
		t.Fatalf("coordinator-send status=%d body=%s", code, redacted(body))
	}
	first := fixture.firstBearer(t)
	if !fixture.controlsProject(t, first) {
		t.Fatal("the coordinator's first token cannot control its project")
	}
	if _, err := fixture.registry.ClearCoordinator("p-codex", "test", time.Time{}); err != nil {
		t.Fatal(err)
	}
	fixture.restartAgenthail(t)
	if fixture.controlsProject(t, first) {
		t.Fatal("project control outlived the lead across a restart")
	}
}

func TestConcurrentResumesShareTheTokenThisProcessHolds(t *testing.T) {
	f := newMCPClaudeFixture(t)
	id, token := f.create(t)
	credentials := f.daemon.mcpCredentials.Load()
	tokens := make([]string, 8)
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Go(func() {
			got, err := credentials.Rotate(id)
			if err != nil {
				t.Error(err)
			}
			tokens[i] = got
		})
	}
	wg.Wait()
	for _, got := range tokens {
		if got != token {
			t.Fatalf("a resume got %s instead of the held token", redacted(got))
		}
	}
	if rows := credentialRows(t, f.registry, id); rows != 1 {
		t.Fatalf("credentials=%d, resumes minted while a token was held", rows)
	}
}

func TestRotationLocksAreReleasedWithTheirLastHolder(t *testing.T) {
	var locks keyedLocks
	release, _ := locks.acquire("a", true)
	if _, ok := locks.acquire("a", false); ok {
		t.Fatal("a held session was locked twice")
	}
	waited := make(chan struct{})
	go func() {
		again, _ := locks.acquire("a", true)
		again()
		close(waited)
	}()
	release()
	<-waited
	for i := range 100 {
		done, _ := locks.acquire(fmt.Sprintf("s-%d", i), true)
		done()
	}
	if size := locks.size(); size != 0 {
		t.Fatalf("locks kept=%d after every holder released", size)
	}
}
