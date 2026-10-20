package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestSessionRowReportsTheLifecycleItsRuntimeSupports(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	adapter := surfaces.NewClaude(nil, home)
	d := New(registry, []surface.Surface{adapter})
	for runtime, want := range map[surface.RuntimeKind]map[string]bool{
		surface.RuntimeClaudeBackground: {"status": true, "logs": true, "stop": true, "resume": true},
		surface.RuntimeClaudeHost:       {"status": true, "logs": false, "stop": true, "resume": true},
		surface.RuntimeClaudeCLI:        {"status": false, "logs": false, "stop": false, "resume": false},
	} {
		row := d.catalogSessionProjection(adapter, surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: runtime, Transport: "uds"}, catalogIdentity{}, time.Now(), "", 0, false, DashboardConfig{})
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Capabilities struct {
				Lifecycle map[string]bool `json:"lifecycle"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Capabilities.Lifecycle) != len(want) {
			t.Fatalf("%s lifecycle=%v, want %v", runtime, decoded.Capabilities.Lifecycle, want)
		}
		for operation, supported := range want {
			if got, ok := decoded.Capabilities.Lifecycle[operation]; !ok || got != supported {
				t.Fatalf("%s lifecycle=%v, want %v", runtime, decoded.Capabilities.Lifecycle, want)
			}
		}
	}
}

func TestResumingABackgroundJobRegistersItsHostSession(t *testing.T) {
	const conversation = "fe758937-0caa-4912-823f-927677775b2b"
	_, registry, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	t.Setenv("HOME", fixture.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture.Transcript(conversation, fixture.Home)
	fixture.Job("fe758937", conversation, fixture.Home, "done", 0)
	if err := registry.RegisterSession(surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeBackground, Cwd: fixture.Home, Status: surface.StatusOffline}); err != nil {
		t.Fatal(err)
	}
	adapter := surfaces.NewClaude(nil, fixture.Home)
	d := New(registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	body, _ := json.Marshal(map[string]string{"action": "session-lifecycle-resume", "sessionId": conversation})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("resume=%d %s", response.Code, response.Body.String())
	}
	if spawns := host.Spawns(t, conversation); len(spawns) != 1 {
		t.Fatalf("spawns=%+v", spawns)
	}
	stored, err := registry.Session(conversation)
	if err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.PID <= 0 {
		t.Fatalf("stored=%+v err=%v, want the host session", stored, err)
	}
	row := d.catalogSessionProjection(adapter, *stored, catalogIdentity{}, time.Now(), "", 0, false, DashboardConfig{})
	if row.Capabilities.Lifecycle != (surface.Lifecycle{Status: true, Stop: true, Resume: true}) {
		t.Fatalf("lifecycle=%+v, want the host's", row.Capabilities.Lifecycle)
	}
}
