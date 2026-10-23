package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	transcript := filepath.Join(home, ".claude", "projects", "-work", "s.jsonl")
	for _, tc := range []struct {
		runtime    surface.RuntimeKind
		transcript string
		want       map[string]bool
	}{
		{surface.RuntimeClaudeBackground, transcript, map[string]bool{"status": true, "logs": true, "stop": true, "resume": true, "adopt": true}},
		{surface.RuntimeClaudeHost, transcript, map[string]bool{"status": true, "logs": true, "stop": true, "resume": true, "adopt": false}},
		{surface.RuntimeClaudeCLI, transcript, map[string]bool{"status": false, "logs": false, "stop": false, "resume": false, "adopt": true}},
		// A job registered before its transcript exists, or a row whose file
		// discovery missed, can still be adopted once the transcript is there.
		{surface.RuntimeClaudeBackground, "", map[string]bool{"status": true, "logs": true, "stop": true, "resume": true, "adopt": true}},
	} {
		runtime, want := tc.runtime, tc.want
		row := d.catalogSessionProjection(adapter, surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: runtime, Transport: "uds", Transcript: tc.transcript, HasLocal: tc.transcript != ""}, catalogIdentity{}, time.Now(), "", 0, nil, false, DashboardConfig{})
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
			t.Fatalf("%s transcript=%q lifecycle=%v, want %v", runtime, tc.transcript, decoded.Capabilities.Lifecycle, want)
		}
		for operation, supported := range want {
			if got, ok := decoded.Capabilities.Lifecycle[operation]; !ok || got != supported {
				t.Fatalf("%s transcript=%q lifecycle=%v, want %v", runtime, tc.transcript, decoded.Capabilities.Lifecycle, want)
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
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	handler := dashboardRouter(d)
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
	if stored.Transport != "uds" {
		t.Fatalf("stored=%+v, want the host process's transport", stored)
	}
}
