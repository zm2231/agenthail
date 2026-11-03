package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type terminalLaunchFixture struct {
	daemon   *Daemon
	registry *registry.Registry
	launcher *launcherFixture
	server   *httptest.Server
	cwd      string
}

func newTerminalLaunchFixture(t *testing.T, launcherID string) *terminalLaunchFixture {
	t.Helper()
	d, r, fake, _, _ := daemonFixture(t)
	d.Surfaces = []surface.Surface{fake}
	launcher := &launcherFixture{id: launcherID, agents: []surface.SurfaceKind{surface.KindCodex}, result: surface.LaunchResult{Location: &surface.Location{Session: "agenthail-1"}}, launchErr: surface.LaunchAcceptedError{Launcher: launcherID, Err: errors.New("outcome is unresolved")}}
	d.SetLaunchers([]surface.Launcher{launcher})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(server.Close)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	return &terminalLaunchFixture{daemon: d, registry: r, launcher: launcher, server: server, cwd: t.TempDir()}
}

func (f *terminalLaunchFixture) launch(t *testing.T, agent string) *surface.LaunchMCPCredential {
	t.Helper()
	w := serveDashboardRequest(dashboardRouter(f.daemon), http.MethodPost, "/api/action", fmt.Sprintf(`{"action":"session-create","surface":%q,"launcher":%q,"message":"hello","cwd":%q}`, agent, f.launcher.id, f.cwd))
	if w.Code != http.StatusAccepted || len(f.launcher.requests) == 0 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	return f.launcher.requests[len(f.launcher.requests)-1].MCP
}

func (f *terminalLaunchFixture) claim(t *testing.T, ref, threadID string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+MCPClaimPath, strings.NewReader(fmt.Sprintf(`{"ref":%q,"threadId":%q}`, ref, threadID)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode
}

func (f *terminalLaunchFixture) refused(t *testing.T, token string) bool {
	t.Helper()
	status, body := postMCP(t, f.server, map[string]string{"Authorization": "Bearer " + token})
	return status == http.StatusUnauthorized && errorCode(body) == "unauthenticated"
}

func TestATerminalCodexLaunchCarriesACredentialThatBindsOnlyThroughItsClaim(t *testing.T) {
	f := newTerminalLaunchFixture(t, surface.LauncherTMUX)
	mcp := f.launch(t, "codex")
	if mcp == nil || mcp.Token == "" || mcp.Ref == "" || mcp.URL != f.server.URL+"/mcp" {
		t.Fatalf("launch credential=%+v", mcp)
	}
	if !f.refused(t, mcp.Token) {
		t.Fatal("a pending credential authenticated")
	}
	if status := f.claim(t, mcp.Ref, "launched-thread"); status != http.StatusOK {
		t.Fatalf("claim status=%d", status)
	}
	if status := f.claim(t, mcp.Ref, "another-thread"); status != http.StatusConflict {
		t.Fatalf("second claim status=%d", status)
	}
	if !f.refused(t, mcp.Token) {
		t.Fatal("a claimed credential authenticated before its session registered")
	}
	f.launcher.locations = map[string]surface.Location{"launched-thread": {Session: "agenthail-1", Pane: "%1"}}
	f.daemon.correlatePendingLaunches(context.Background(), []surface.Session{{ID: "launched-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: f.cwd, Status: surface.StatusIdle}})
	if whoami, refused := callMCP(t, mcpSession(t, f.server, mcp.Token), "agenthail_whoami", nil); refused || whoami["sessionId"] != "launched-thread" {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
}

func TestAnUnclaimedTerminalLaunchCredentialIsDeletedOnceThePendingLaunchExpires(t *testing.T) {
	f := newTerminalLaunchFixture(t, surface.LauncherCMUX)
	mcp := f.launch(t, "codex")
	if mcp == nil {
		t.Fatal("cmux launch carried no credential")
	}
	if err := f.registry.PrunePendingCredentials(time.Now().Add(pendingLaunchLifetime + time.Second)); err != nil {
		t.Fatal(err)
	}
	if status := f.claim(t, mcp.Ref, "late-thread"); status != http.StatusConflict {
		t.Fatalf("claim after expiry status=%d", status)
	}
	if !f.refused(t, mcp.Token) {
		t.Fatal("an expired launch credential authenticated")
	}
}

func TestOnlyTerminalCodexLaunchesCarryALaunchCredential(t *testing.T) {
	f := newTerminalLaunchFixture(t, surface.LauncherTMUX)
	credentials := f.daemon.mcpCredentials.Load()
	for _, tc := range []struct {
		launcher string
		agent    surface.SurfaceKind
	}{{surface.LauncherTMUX, surface.KindClaude}, {surface.LauncherCodexAppServer, surface.KindCodex}} {
		if mcp, err := credentials.terminalLaunch(tc.launcher, tc.agent); err != nil || mcp != nil {
			t.Fatalf("%s %s carried %+v err=%v", tc.launcher, tc.agent, mcp, err)
		}
	}
}
