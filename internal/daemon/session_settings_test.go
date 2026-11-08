package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type settingsSurface struct {
	*daemonSurface
	settings surface.RuntimeSettings
}

func (s *settingsSurface) SessionSettings(context.Context, *surface.Session) (surface.RuntimeSettings, error) {
	return s.settings, nil
}

func serveSessionSettings(t *testing.T, d *Daemon, id string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-settings?id="+id, nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	dashboardRouter(d).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func secretKeys(value any) []string {
	var found []string
	switch v := value.(type) {
	case map[string]any:
		for key, inner := range v {
			lower := strings.ToLower(key)
			for _, secret := range []string{"token", "env", "credential", "password", "secret", "cookie"} {
				if strings.Contains(lower, secret) {
					found = append(found, key)
				}
			}
			found = append(found, secretKeys(inner)...)
		}
	case []any:
		for _, inner := range v {
			found = append(found, secretKeys(inner)...)
		}
	}
	return found
}

func TestSessionSettingsSayWhereEachValueCameFromAndNameAMissingRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	work := filepath.Join(home, ".work", ".claude")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := surface.WriteAgenthailConfig(home, map[string]json.RawMessage{"claudeConfigDirs": mustJSON(t, []string{work})}); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	session := surface.Session{ID: "worker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Cwd: home, Transport: "host", ConfiguredModel: "opus", Transcript: filepath.Join(work, "projects", "p", "worker.jsonl")}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('worker','{"permission":"edits","roots":["/work"]}')`); err != nil {
		t.Fatal(err)
	}
	fake := &settingsSurface{
		daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"worker": session}, observations: map[string]*surface.TurnObservation{}},
		settings: surface.RuntimeSettings{
			Effort:      &surface.SettingValue{Value: "high", Source: surface.SettingFromRuntime},
			Permission:  &surface.PermissionSetting{Mode: "acceptEdits", Source: surface.SettingFromTranscript},
			Unavailable: map[string]string{"model": "no reply names a model yet"},
		},
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})

	body := serveSessionSettings(t, d, "worker")
	want := map[string]any{
		"sessionId": "worker", "surface": "claude", "runtimeKind": string(surface.RuntimeClaudeHost), "transport": "host", "cwd": home,
		"model":      map[string]any{"value": "opus", "source": "started"},
		"effort":     map[string]any{"value": "high", "source": "runtime"},
		"permission": map[string]any{"mode": "acceptEdits", "source": "transcript"},
		"configRoot": map[string]any{"name": "work", "root": work, "source": "config"},
		"ceiling":    map[string]any{"permission": "edits", "roots": []any{"/work"}},
	}
	if got, wanted := mustJSON(t, body), mustJSON(t, want); string(got) != string(wanted) {
		t.Fatalf("settings = %s\nwant %s", got, wanted)
	}
	if keys := secretKeys(body); len(keys) > 0 {
		t.Fatalf("the read carries secret-looking keys %v", keys)
	}

	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	root, _ := serveSessionSettings(t, d, "worker")["configRoot"].(map[string]any)
	if root["root"] != work || root["missing"] != true {
		t.Fatalf("a root deleted after start: configRoot=%v, want it named and missing", root)
	}
}

func TestSessionSettingsAreNullWithAReasonWhereNothingReportsThem(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	body := serveSessionSettings(t, newTestDaemon(t, reg, []surface.Surface{fake}), from.ID)
	for _, field := range []string{"model", "effort", "permission", "configRoot", "ceiling"} {
		if value, ok := body[field]; !ok || value != nil {
			t.Fatalf("%s = %v (present=%v), want null", field, value, ok)
		}
	}
	unavailable, _ := body["unavailable"].(map[string]any)
	for _, field := range []string{"model", "effort", "permission", "configRoot"} {
		if reason, _ := unavailable[field].(string); reason == "" {
			t.Fatalf("unavailable = %v, want a reason for %s", unavailable, field)
		}
	}
}

func TestAnAgentReadsASessionsSettingsWithoutASetting(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if _, err := f.r.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('worker','{"permission":"edits"}')`); err != nil {
		t.Fatal(err)
	}
	out, refused := callMCP(t, f.lead, "agenthail_session_settings", map[string]any{"target": "worker"})
	if refused {
		t.Fatalf("reading the worker's settings: %v", out)
	}
	if ceiling, _ := out["ceiling"].(map[string]any); out["sessionId"] != "worker" || ceiling["permission"] != "edits" {
		t.Fatalf("read = %v", out)
	}
	if code := refusalCode(t, f.lead, "agenthail_session_settings", map[string]any{"target": "worker", "value": "opus"}); code != "invalid" {
		t.Fatalf("a value without a setting: code=%q", code)
	}
}
