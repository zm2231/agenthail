package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
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

func TestAnAgentNotInstalledIsUnavailableAndOneTurnedOffIsNeitherDiscoveredNorStarted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, fake, _, _ := daemonFixture(t)
	installed := false
	d.Surfaces = []surface.Surface{localDaemonAgent{daemonSurface: fake, installed: &installed}}
	handler := dashboardRouter(d)
	ctx := context.Background()

	agents := func() []surface.AgentStatus {
		t.Helper()
		response := serveDashboardRequest(handler, http.MethodGet, "/api/settings", "")
		var settings struct {
			Agents []surface.AgentStatus `json:"agents"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &settings) != nil {
			t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
		}
		return settings.Agents
	}
	health := func() string {
		t.Helper()
		d.discoverCatalogOf(ctx, nil)
		entry, _ := d.observedSurfaceHealth(surface.KindCodex)
		return entry.Health
	}

	if listed := agents(); len(listed) != 1 || listed[0].ID != "codex" || listed[0].Mode != "auto" || listed[0].State != surface.AgentUnavailable {
		t.Fatalf("agents=%+v", listed)
	}
	if got := health(); got != "agent_unavailable" {
		t.Fatalf("a missing Codex on auto reads %q", got)
	}

	offered := func() bool {
		t.Helper()
		options := serveDashboardRequest(handler, http.MethodGet, "/api/v1/session-options", "")
		if options.Code != http.StatusOK {
			t.Fatalf("session options status=%d body=%s", options.Code, options.Body.String())
		}
		return strings.Contains(options.Body.String(), `"id":"codex"`)
	}
	if offered() {
		t.Fatal("an agent not installed is offered for new sessions")
	}
	installed = true
	if !offered() || health() != "healthy" {
		t.Fatal("an installed agent on auto is not offered or not healthy")
	}

	if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", `{"action":"agent-mode","agent":"codex","mode":"off"}`); response.Code != http.StatusOK {
		t.Fatalf("turn off: status=%d body=%s", response.Code, response.Body.String())
	}
	if listed := agents(); listed[0].Mode != "off" || listed[0].State != surface.AgentOff {
		t.Fatalf("agents=%+v", listed)
	}
	if got := health(); got != "agent_off" {
		t.Fatalf("a Codex turned off reads %q", got)
	}
	if offered() {
		t.Fatal("an installed agent turned off is still offered")
	}
	response := serveDashboardRequest(handler, http.MethodPost, "/api/action", `{"action":"session-create","surface":"codex","message":"hello"}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Codex is turned off") {
		t.Fatalf("start while off: status=%d body=%s", response.Code, response.Body.String())
	}

	for _, body := range []string{`{"action":"agent-mode","agent":"codex","mode":"disabled"}`, `{"action":"agent-mode","agent":"notion","mode":"off"}`} {
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}

	installed = false
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", `{"action":"agent-mode","agent":"codex","mode":"on"}`); response.Code != http.StatusOK {
		t.Fatalf("turn on: status=%d body=%s", response.Code, response.Body.String())
	}
	if listed := agents(); listed[0].State != surface.AgentMissing {
		t.Fatalf("agents=%+v", listed)
	}
	if got := health(); got != "unavailable" {
		t.Fatalf("a Codex turned on but missing reads %q", got)
	}
}
