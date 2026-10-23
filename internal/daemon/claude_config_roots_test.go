package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestClaudeConfigRootsReachSessionOptionsCreationAndTheSessionRow(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	t.Setenv("HOME", host.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(host.Home, ".work", ".claude")
	for path, content := range map[string]string{
		filepath.Join(host.Home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + second + `"]}`,
		filepath.Join(host.Home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"a","emailAddress":"me@example.com"}}`,
		filepath.Join(second, ".claude.json"):                 `{"oauthAccount":{"accountUuid":"b","emailAddress":"work@example.com"}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := newTestDaemon(t, registry, []surface.Surface{surfaces.NewClaude(nil, host.Home)})
	handler := dashboardRouter(d)
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	var options struct {
		ClaudeConfigRoots []surface.ClaudeConfigRoot `json:"claudeConfigRoots"`
	}
	response := call(http.MethodGet, "/api/v1/session-options", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &options); err != nil || len(options.ClaudeConfigRoots) != 2 {
		t.Fatalf("options=%s err=%v", response.Body.String(), err)
	}
	if work := options.ClaudeConfigRoots[1]; work.Name != "work" || work.SignedInAs == nil || work.SignedInAs.Email != "work@example.com" || work.Primary {
		t.Fatalf("work=%+v", work)
	}

	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "hello", "cwd": host.Cwd, "configRoot": "work"})
	response = call(http.MethodPost, "/api/v1/actions", body)
	var receipt struct {
		Session surface.Session `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || response.Code != http.StatusCreated {
		t.Fatalf("create=%s err=%v", response.Body.String(), err)
	}
	spawns := host.Spawns(t, receipt.Session.ID)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != second {
		t.Fatalf("spawns=%+v, want one start in %s", spawns, second)
	}

	transcript := filepath.Join(second, "projects", "-work", receipt.Session.ID+".jsonl")
	row := d.catalogSessionProjection(d.Surfaces[0], surface.Session{ID: receipt.Session.ID, Surface: surface.KindClaude, Transcript: transcript}, catalogIdentity{}, receipt.Session.LastActive, "", 0, nil, false, DashboardConfig{})
	if row.ClaudeConfigRoot == nil || row.ClaudeConfigRoot.Name != "work" || row.ClaudeConfigRoot.SignedInAs != "work@example.com" || row.ClaudeConfigRoot.Root != second {
		t.Fatalf("row config root=%+v", row.ClaudeConfigRoot)
	}
	codex := d.catalogSessionProjection(d.Surfaces[0], surface.Session{ID: "c", Surface: surface.KindCodex, Transcript: transcript}, catalogIdentity{}, receipt.Session.LastActive, "", 0, nil, false, DashboardConfig{})
	if codex.ClaudeConfigRoot != nil {
		t.Fatalf("a Codex row carries a Claude config root: %+v", codex.ClaudeConfigRoot)
	}
}
