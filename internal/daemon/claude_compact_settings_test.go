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
		filepath.Join(primary, "settings.json"):          `{"theme":"dark","autoCompactWindow":400000}`,
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
	if roots := read(); len(roots) != 2 || roots[0].Root != primary || roots[0].AutoCompactWindow.Tokens != 400000 || roots[1].Root != work || roots[1].AutoCompactWindow != nil {
		t.Fatalf("roots=%+v", roots)
	}

	response := serveDashboardRequest(router, http.MethodPost, "/api/claude/compact-settings", `{"root":"claude-work","model":"claude-opus-5-5","window":"250000","idleCompaction":false}`)
	var written surface.ClaudeCompactSettings
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &written) != nil || written.Root != work || written.ModelWindows["claude-opus-5-5"].Tokens != 250000 || written.IdleCompaction == nil || *written.IdleCompaction {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if roots := read(); roots[0].AutoCompactWindow.Tokens != 400000 || roots[1].ModelWindows["claude-opus-5-5"].Tokens != 250000 {
		t.Fatalf("roots=%+v", roots)
	}
	for _, body := range []string{`{"window":"50000"}`, `{"root":"/not/a/root","window":"300000"}`, `{"model":"claude-opus-5-5"}`, `{"window":"300000"} trailing`, `{"window":"300000"}{"window":"auto"}`, `{"window":"300000","idle":false}`, `{"window":"300000"}` + strings.Repeat(" ", 16<<10) + `{"window":"auto"}`} {
		if response := serveDashboardRequest(router, http.MethodPost, "/api/claude/compact-settings", body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}
	if data, _ := os.ReadFile(filepath.Join(primary, "settings.json")); string(data) != `{"theme":"dark","autoCompactWindow":400000}` {
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

func TestAnIdleCompactionHoldIsListedUnderTheConfiguredRootItCameFromWithTheSessionsName(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	work := filepath.Join(home, ".claude-work")
	linked := filepath.Join(home, "linked-work")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+work+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(work, linked); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("planner", from.ID); err != nil {
		t.Fatal(err)
	}
	d.idleHolds.record(IdleCompactionHold{SessionID: from.ID, Root: linked, ContextTokens: 250000, Reason: "a background task is running"}, func(IdleCompactionHold) {})
	response := serveDashboardRequest(dashboardRouter(d), http.MethodGet, "/api/claude/compact-settings", "")
	var body struct {
		Holds []IdleCompactionHold `json:"idleCompactionHolds"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Holds) != 1 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if hold := body.Holds[0]; hold.Root != work || hold.Session != "@planner" || hold.SessionID != from.ID {
		t.Fatalf("hold=%+v, want root %s and session @planner", hold, work)
	}
}

func TestAgenthailsIdleCompactionIsOnByDefaultAndSetPerRootInItsOwnConfig(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	primary := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", primary)
	work := filepath.Join(home, ".claude-work")
	config := filepath.Join(home, ".agenthail", "config.json")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"claudeConfigDirs":["`+work+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	router := dashboardRouter(d)
	read := func() map[string]surface.IdleCompaction {
		t.Helper()
		response := serveDashboardRequest(router, http.MethodGet, "/api/claude/compact-settings", "")
		var body struct {
			Roots []surface.ClaudeCompactSettings `json:"roots"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		settings := map[string]surface.IdleCompaction{}
		for _, root := range body.Roots {
			settings[root.Root] = root.AgenthailIdle
		}
		return settings
	}
	on := surface.IdleCompaction{Enabled: true, MinContextTokens: surface.DefaultIdleCompactionMinTokens}
	if got := read(); got[primary] != on || got[work] != on {
		t.Fatalf("defaults = %+v, want on from %d tokens under every root", got, surface.DefaultIdleCompactionMinTokens)
	}

	if response := serveDashboardRequest(router, http.MethodPost, "/api/claude/compact-settings", `{"root":"claude-work","agenthailIdleCompaction":false,"agenthailIdleMinTokens":300000}`); response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if got := read(); got[primary] != on || got[work] != (surface.IdleCompaction{Enabled: false, MinContextTokens: 300000}) {
		t.Fatalf("after turning it off under the work root: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(work, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("Agenthail's own setting wrote Claude's settings file: %v", err)
	}
	before, _ := os.ReadFile(config)
	if !strings.Contains(string(before), `"claudeConfigDirs"`) {
		t.Fatalf("the config lost its other keys: %s", before)
	}
	if response := serveDashboardRequest(router, http.MethodPost, "/api/claude/compact-settings", `{"root":"claude-work","agenthailIdleCompaction":true,"agenthailIdleMinTokens":50000}`); response.Code != http.StatusBadRequest {
		t.Fatalf("a threshold under the minimum: status=%d body=%s", response.Code, response.Body.String())
	}
	if after, _ := os.ReadFile(config); string(after) != string(before) {
		t.Fatalf("a refused change wrote the config:\n%s", after)
	}
}
