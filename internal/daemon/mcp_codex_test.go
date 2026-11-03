package daemon

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type mcpCodexFixture struct {
	registry  *registry.Registry
	lifecycle *codextest.Lifecycle
	daemon    *Daemon
	server    *httptest.Server
}

func codexBearer(params map[string]any) string {
	config, _ := params["config"].(map[string]any)
	servers, _ := config["mcp_servers"].(map[string]any)
	server, _ := servers["agenthail"].(map[string]any)
	headers, _ := server["http_headers"].(map[string]any)
	value, _ := headers["Authorization"].(string)
	return strings.TrimPrefix(value, "Bearer ")
}

func newMCPCodexFixture(t *testing.T) *mcpCodexFixture {
	t.Helper()
	fixture := &mcpCodexFixture{}
	home := codextest.Home(t)
	fixture.lifecycle = codextest.InstallLifecycle(t, home, func(method string, params map[string]any) map[string]any {
		if method != "mcpServerStatus/list" {
			return nil
		}
		threadID, _ := params["threadId"].(string)
		if fixture.loadedWithServer(threadID) {
			return map[string]any{"data": []any{map[string]any{"name": "agenthail", "runtimeStatus": "connected"}}}
		}
		return map[string]any{"data": []any{}}
	})
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fixture.registry = r
	fixture.restartAgenthail(t)
	return fixture
}

// The fake app-server connects a thread's MCP server the way Codex does: from the config of the call that loaded it.
func (f *mcpCodexFixture) loadedWithServer(threadID string) bool {
	bearer := ""
	daemon := f.lifecycle.Daemon()
	for _, method := range []string{"thread/start", "thread/resume"} {
		for _, call := range daemon.Calls(method) {
			if id, _ := call["threadId"].(string); id == threadID || method == "thread/start" {
				bearer = codexBearer(call)
			}
		}
	}
	return bearer != ""
}

func (f *mcpCodexFixture) restartAgenthail(t *testing.T) {
	t.Helper()
	if f.server != nil {
		f.server.Close()
	}
	f.daemon = newTestDaemon(t, f.registry, []surface.Surface{surfaces.NewCodex(unreachableDesktop)})
	f.server = httptest.NewServer(f.daemon.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(f.server.Close)
	f.daemon.attachMCPCredentials(strings.TrimPrefix(f.server.URL, "http://"))
}

func (f *mcpCodexFixture) action(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	handler := &headlessCodexFixture{registry: f.registry, lifecycle: f.lifecycle, handler: dashboardRouter(f.daemon)}
	return handler.action(t, body)
}

func (f *mcpCodexFixture) whoami(t *testing.T, token string) (map[string]any, bool) {
	t.Helper()
	return callMCP(t, mcpSession(t, f.server, token), "agenthail_whoami", nil)
}

func TestACreatedCodexThreadStartsWithItsOwnCredential(t *testing.T) {
	fixture := newMCPCodexFixture(t)
	code, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir()})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if code >= 300 || id == "" {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	starts := fixture.lifecycle.Daemon().Calls("thread/start")
	if len(starts) != 1 {
		t.Fatalf("thread/start=%s", redacted(starts))
	}
	token := codexBearer(starts[0])
	if !strings.HasPrefix(token, registry.SessionTokenPrefix) {
		t.Fatalf("thread/start carried no credential: %s", redacted(starts[0]["config"]))
	}
	if whoami, refused := fixture.whoami(t, token); refused || whoami["sessionId"] != id {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
}

func TestAResumedCodexThreadKeepsItsCredentialWhileTheDaemonHoldsIt(t *testing.T) {
	fixture := newMCPCodexFixture(t)
	_, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First", "cwd": t.TempDir()})
	id := created["session"].(map[string]any)["id"].(string)
	started := codexBearer(fixture.lifecycle.Daemon().Calls("thread/start")[0])
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")
	fixture.lifecycle.Daemon().Unload(id)
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Again"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	resumes := fixture.lifecycle.Daemon().Calls("thread/resume")
	if len(resumes) != 1 || codexBearer(resumes[0]) != started {
		t.Fatalf("resumes=%d, the last did not carry the start credential", len(resumes))
	}
	if whoami, refused := fixture.whoami(t, started); refused || whoami["sessionId"] != id {
		t.Fatalf("whoami=%v", whoami)
	}
}

func TestALoadedCodexThreadWhosePermissionsChangeIsReloadedWithItsNewCredentialOnItsNextInput(t *testing.T) {
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
	started := codexBearer(fixture.lifecycle.Daemon().Calls("thread/start")[0])
	if code, body := fixture.action(t, map[string]any{"action": "coordinator-set", "sessionId": id, "name": "captain"}); code != http.StatusOK {
		t.Fatalf("coordinator-set status=%d body=%v", code, body)
	}
	if status, body := postMCP(t, fixture.server, map[string]string{"Authorization": "Bearer " + started}); status != http.StatusUnauthorized || errorCode(body) != "credential_revoked" {
		t.Fatalf("old credential status=%d body=%v", status, body)
	}
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")
	unsubscribed := len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe"))
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Again"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	resumes := fixture.lifecycle.Daemon().Calls("thread/resume")
	if len(resumes) == 0 || len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe")) != unsubscribed+1 {
		t.Fatalf("the loaded thread was not reloaded with its new credential: resumes=%d", len(resumes))
	}
	rotated := codexBearer(resumes[len(resumes)-1])
	if rotated == "" || rotated == started {
		t.Fatalf("resume carried %s", redacted(rotated))
	}
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Once more"}); code != http.StatusOK || len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe")) != unsubscribed+1 {
		t.Fatalf("a second send reloaded the thread again: status=%d body=%v", code, body)
	}
	whoami, refused := fixture.whoami(t, rotated)
	capabilities, _ := whoami["capabilities"].([]any)
	if refused || !slices.Contains(capabilities, any("control.project")) {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
}

func TestACodexCoordinatorStartedByItsFirstMessageStartsWithItsLeadCredential(t *testing.T) {
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
	if code, body := fixture.action(t, map[string]any{"action": "coordinator-send", "name": "captain", "message": "hello"}); code != http.StatusOK || body["started"] != true {
		t.Fatalf("coordinator-send status=%d body=%s", code, redacted(body))
	}
	starts := fixture.lifecycle.Daemon().Calls("thread/start")
	if len(starts) != 1 {
		t.Fatalf("thread/start calls=%d", len(starts))
	}
	first := codexBearer(starts[0])
	whoami, refused := fixture.whoami(t, first)
	capabilities, _ := whoami["capabilities"].([]any)
	if refused || !slices.Contains(capabilities, any("control.project")) {
		t.Fatalf("refused=%v, the coordinator's first credential cannot control its project", refused)
	}
	if unsubscribes := len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe")); unsubscribes != 0 {
		t.Fatalf("the coordinator was reloaded %d times after its first turn began", unsubscribes)
	}
}

func TestAfterAnAgenthailRestartAResumeThatLoadsTheThreadBareRotatesAndReattaches(t *testing.T) {
	fixture := newMCPCodexFixture(t)
	_, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First", "cwd": t.TempDir()})
	id := created["session"].(map[string]any)["id"].(string)
	started := codexBearer(fixture.lifecycle.Daemon().Calls("thread/start")[0])
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")
	fixture.lifecycle.Daemon().Unload(id)
	fixture.restartAgenthail(t)
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "After restart"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	resumes := fixture.lifecycle.Daemon().Calls("thread/resume")
	if len(resumes) != 2 || codexBearer(resumes[0]) != "" {
		t.Fatalf("resumes=%d first bearer=%s", len(resumes), redacted(codexBearer(resumes[0])))
	}
	rotated := codexBearer(resumes[1])
	if rotated == "" || rotated == started || len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe")) != 1 {
		t.Fatalf("re-attach bearer=%s unsubscribes=%d", redacted(rotated), len(fixture.lifecycle.Daemon().Calls("thread/unsubscribe")))
	}
	if status, body := postMCP(t, fixture.server, map[string]string{"Authorization": "Bearer " + started}); status != http.StatusUnauthorized || errorCode(body) != "credential_revoked" {
		t.Fatalf("old credential status=%d body=%v", status, body)
	}
	if whoami, refused := fixture.whoami(t, rotated); refused || whoami["sessionId"] != id {
		t.Fatalf("whoami=%v", whoami)
	}
}
