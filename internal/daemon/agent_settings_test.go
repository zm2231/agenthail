package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

type localDaemonAgent struct {
	*daemonSurface
	installed *bool
}

func (localDaemonAgent) AgentLabel() string { return "Codex" }
func (a localDaemonAgent) LocateProgram() error {
	if *a.installed {
		return nil
	}
	return errors.New("no codex command")
}

type agentSettingsClient struct {
	t       *testing.T
	d       *Daemon
	handler http.Handler
}

func (c agentSettingsClient) agents() []surface.AgentStatus {
	c.t.Helper()
	response := serveDashboardRequest(c.handler, http.MethodGet, "/api/settings", "")
	var settings struct {
		Agents []surface.AgentStatus `json:"agents"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &settings) != nil {
		c.t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
	return settings.Agents
}

func (c agentSettingsClient) health(kind surface.SurfaceKind) string {
	c.t.Helper()
	c.d.discoverCatalogOf(context.Background(), nil)
	entry, _ := c.d.observedSurfaceHealth(kind)
	return entry.Health
}

func (c agentSettingsClient) offered(id string) bool {
	c.t.Helper()
	options := serveDashboardRequest(c.handler, http.MethodGet, "/api/v1/session-options", "")
	if options.Code != http.StatusOK {
		c.t.Fatalf("session options status=%d body=%s", options.Code, options.Body.String())
	}
	return strings.Contains(options.Body.String(), `"id":"`+id+`"`)
}

func (c agentSettingsClient) set(agent, mode string) *httptest.ResponseRecorder {
	return serveDashboardRequest(c.handler, http.MethodPost, "/api/settings", `{"action":"agent-mode","agent":"`+agent+`","mode":"`+mode+`"}`)
}

func TestOnlyAnAgentTurnedOffIsLeftOutAndOneWithoutItsProgramIsUnavailableOnlyWhenUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, fake, _, _ := daemonFixture(t)
	installed := false
	d.Surfaces = []surface.Surface{localDaemonAgent{daemonSurface: fake, installed: &installed}}
	c := agentSettingsClient{t: t, d: d, handler: dashboardRouter(d)}

	if listed := c.agents(); len(listed) != 1 || listed[0].ID != "codex" || listed[0].Mode != "auto" || listed[0].State != surface.AgentUnavailable {
		t.Fatalf("agents=%+v", listed)
	}
	if got := c.health(surface.KindCodex); got != "healthy" || !c.offered("codex") {
		t.Fatalf("an agent reached without its program on PATH reads %q or is not offered", got)
	}
	fake.listErr = errors.New("Codex session discovery is unavailable")
	if got := c.health(surface.KindCodex); got != "agent_unavailable" {
		t.Fatalf("an unreachable agent without its program reads %q", got)
	}
	fake.listErr = nil

	if response := c.set("codex", "off"); response.Code != http.StatusOK {
		t.Fatalf("turn off: status=%d body=%s", response.Code, response.Body.String())
	}
	if listed := c.agents(); listed[0].Mode != "off" || listed[0].State != surface.AgentOff {
		t.Fatalf("agents=%+v", listed)
	}
	listings := fake.listCalls.Load()
	if got := c.health(surface.KindCodex); got != "agent_off" || fake.listCalls.Load() != listings {
		t.Fatalf("a Codex turned off reads %q, or was listed", got)
	}
	if c.offered("codex") {
		t.Fatal("an agent turned off is still offered")
	}
	response := serveDashboardRequest(c.handler, http.MethodPost, "/api/action", `{"action":"session-create","surface":"codex","message":"hello"}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Codex is turned off") {
		t.Fatalf("start while off: status=%d body=%s", response.Code, response.Body.String())
	}

	if err := os.WriteFile(dashconfig.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	response = serveDashboardRequest(c.handler, http.MethodPost, "/api/action", `{"action":"session-create","surface":"codex","message":"hello"}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "cannot be read") {
		t.Fatalf("start with unreadable settings: status=%d body=%s", response.Code, response.Body.String())
	}
	if err := os.Remove(dashconfig.Path()); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{`{"action":"agent-mode","agent":"codex","mode":"disabled"}`, `{"action":"agent-mode","agent":"notion","mode":"off"}`} {
		if response := serveDashboardRequest(c.handler, http.MethodPost, "/api/settings", body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}

	if response := c.set("codex", "on"); response.Code != http.StatusOK {
		t.Fatalf("turn on: status=%d body=%s", response.Code, response.Body.String())
	}
	if listed := c.agents(); listed[0].State != surface.AgentMissing {
		t.Fatalf("agents=%+v", listed)
	}
	if got := c.health(surface.KindCodex); got != "unavailable" || !c.offered("codex") {
		t.Fatalf("a Codex turned on but missing reads %q, or is no longer offered", got)
	}
}

func TestCodexAndClaudeWithNeitherOnPathAreStillOfferedUntilTurnedOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("AGENTHAIL_CLAUDE_BIN", "")
	_, registry, _, _, _ := daemonFixture(t)
	codex := codexsurface.NewCodexIn(codexsurface.CodexHome{Codex: filepath.Join(home, ".codex"), State: filepath.Join(home, ".agenthail")}, "")
	d := newTestDaemon(t, registry, []surface.Surface{claudesurface.NewClaude(nil, home), codex})
	c := agentSettingsClient{t: t, d: d, handler: dashboardRouter(d)}

	for _, status := range c.agents() {
		if status.Mode != "auto" || status.State != surface.AgentUnavailable {
			t.Fatalf("with neither on PATH, %s reads %+v", status.ID, status)
		}
	}
	if !c.offered("claude") || !c.offered("codex") {
		t.Fatal("an agent left on auto is dropped from new sessions because its command is not on PATH")
	}
	if response := c.set("claude", "off"); response.Code != http.StatusOK {
		t.Fatalf("turn off: status=%d body=%s", response.Code, response.Body.String())
	}
	if c.offered("claude") || !c.offered("codex") {
		t.Fatal("turning Claude off must leave it out and keep Codex")
	}
}
