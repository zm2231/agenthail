package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestTheAppSetsRemoteControlAtStartupPerClaudeRootAndRefusesABrokenSettingsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	primary := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", primary)
	work := filepath.Join(home, ".claude-work")
	broken := `{"theme":`
	for path, content := range map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + work + `"]}`,
		filepath.Join(primary, "settings.json"):          `{"theme":"dark","hooks":{"Stop":[]}}`,
		filepath.Join(work, "settings.json"):             broken,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0640); err != nil {
			t.Fatal(err)
		}
	}
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	claude := surfaces.NewClaude(nil, home)
	router := dashboardRouter(newTestDaemon(t, r, []surface.Surface{claude}))
	call := func(method, body string) (int, struct {
		Available bool                          `json:"available"`
		Roots     []surface.ClaudeRemoteControl `json:"roots"`
	}, string) {
		t.Helper()
		request := httptest.NewRequest(method, "/api/v1/claude/remote-control", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var parsed struct {
			Available bool                          `json:"available"`
			Roots     []surface.ClaudeRemoteControl `json:"roots"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &parsed)
		return recorder.Code, parsed, recorder.Body.String()
	}

	code, listing, raw := call(http.MethodGet, "")
	if code != http.StatusOK || !listing.Available || len(listing.Roots) != 2 || listing.Roots[0].AtStartup != nil || listing.Roots[1].Problem == "" {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	code, listing, raw = call(http.MethodPost, `{"root":"`+primary+`","atStartup":true}`)
	if code != http.StatusOK || listing.Roots[0].AtStartup == nil || !*listing.Roots[0].AtStartup {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	data, _ := os.ReadFile(filepath.Join(primary, "settings.json"))
	if string(data) != `{"theme":"dark","hooks":{"Stop":[]},"remoteControlAtStartup":true}` {
		t.Fatalf("settings=%s", data)
	}
	if info, err := os.Stat(filepath.Join(primary, "settings.json")); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("mode changed: %v %v", info.Mode().Perm(), err)
	}

	code, _, raw = call(http.MethodPost, `{"root":"`+work+`","atStartup":true}`)
	if code != http.StatusUnprocessableEntity || !strings.Contains(raw, "not a JSON object") {
		t.Fatalf("a broken settings file: status=%d body=%s", code, raw)
	}
	if data, _ := os.ReadFile(filepath.Join(work, "settings.json")); string(data) != broken {
		t.Fatalf("a broken settings file was overwritten: %s", data)
	}
	for _, body := range []string{`{"root":"` + primary + `"}`, `{"root":"/not/a/root","atStartup":true}`, `{"atStartup":"yes"}`} {
		if code, _, raw := call(http.MethodPost, body); code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", body, code, raw)
		}
	}

	claude.DisableRemoteControl()
	if code, listing, raw := call(http.MethodGet, ""); code != http.StatusOK || listing.Available {
		t.Fatalf("an install without Remote Control: status=%d body=%s", code, raw)
	}
	if code, _, raw := call(http.MethodPost, `{"root":"`+primary+`","atStartup":false}`); code != http.StatusConflict {
		t.Fatalf("an install without Remote Control set it: status=%d body=%s", code, raw)
	}
}
