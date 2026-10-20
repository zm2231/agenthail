package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheDashboardReadsAndEditsEachClaudeRootsCompactionSettings(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	primary := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", primary)
	work := filepath.Join(home, ".claude-work")
	for path, content := range map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + work + `"]}`,
		filepath.Join(primary, "settings.json"):          `{"theme":"dark","autoCompactWindow":425000}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	router := dashboardRouter(d)
	read := func() []surface.ClaudeCompactSettings {
		t.Helper()
		response := serveDashboardRequest(router, http.MethodGet, "/api/claude/compact-settings", "")
		var body struct {
			Roots []surface.ClaudeCompactSettings `json:"roots"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return body.Roots
	}
	if roots := read(); len(roots) != 2 || roots[0].Root != primary || roots[0].AutoCompactWindow.Tokens != 425000 || roots[1].Root != work || roots[1].AutoCompactWindow != nil {
		t.Fatalf("roots=%+v", roots)
	}

	response := serveDashboardRequest(router, http.MethodPost, "/api/claude/compact-settings", `{"root":"claude-work","model":"claude-opus-5-5","window":"250000","idleCompaction":false}`)
	var written surface.ClaudeCompactSettings
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &written) != nil || written.Root != work || written.ModelWindows["claude-opus-5-5"].Tokens != 250000 || written.IdleCompaction == nil || *written.IdleCompaction {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if roots := read(); roots[0].AutoCompactWindow.Tokens != 425000 || roots[1].ModelWindows["claude-opus-5-5"].Tokens != 250000 {
		t.Fatalf("roots=%+v", roots)
	}
	for _, body := range []string{`{"window":"50000"}`, `{"root":"/not/a/root","window":"300000"}`, `{"model":"claude-opus-5-5"}`, `{"window":"300000"} trailing`, `{"window":"300000"}{"window":"auto"}`, `{"window":"300000","idle":false}`} {
		if response := serveDashboardRequest(router, http.MethodPost, "/api/claude/compact-settings", body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}
	if data, _ := os.ReadFile(filepath.Join(primary, "settings.json")); string(data) != `{"theme":"dark","autoCompactWindow":425000}` {
		t.Fatalf("a refused change wrote the file: %s", data)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/claude/compact-settings", strings.NewReader(`{"window":"auto"}`))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"auto":true`) {
		t.Fatalf("v1 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
