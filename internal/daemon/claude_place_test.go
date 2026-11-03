package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type pickFixture struct {
	t       *testing.T
	host    *claudehosttest.Fixture
	second  string
	handler http.Handler
	reads   *atomic.Int32
	delay   *atomic.Int64
	daemon  *Daemon
}

func newPickFixture(t *testing.T, twoRoots bool) pickFixture {
	_, registry, _, _, _ := daemonFixture(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	t.Setenv("HOME", host.Home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(host.Home, ".work", ".claude")
	files := map[string]string{
		filepath.Join(host.Home, ".claude.json"): `{"oauthAccount":{"accountUuid":"acct-a","emailAddress":"me@example.com","organizationUuid":"org-a"}}`,
	}
	if twoRoots {
		files[filepath.Join(host.Home, ".agenthail", "config.json")] = `{"claudeConfigDirs":["` + second + `"]}`
		files[filepath.Join(second, ".claude.json")] = `{"oauthAccount":{"accountUuid":"acct-b","emailAddress":"work@example.com","organizationUuid":"org-b"}}`
	}
	writeUsageFixture(t, files)
	resets := time.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
	percent := map[string]string{"org-a": "80", "org-b": "10"}
	profiles := []signin.Source{{ProfileID: "sp_a", Label: "a", Browser: signin.Chrome, Dir: "/a"}, {ProfileID: "sp_b", Label: "b", Browser: signin.Chrome, Dir: "/b"}}
	reads, delay := &atomic.Int32{}, &atomic.Int64{}
	claude := surfaces.NewClaudeWithRequest(candidates(profiles), host.Home, func(ctx context.Context, _ string, url string, _ map[string]string, _ string, cookies surfaces.CookieSource, _ time.Duration) (int, string, error) {
		reads.Add(1)
		select {
		case <-time.After(time.Duration(delay.Load())):
		case <-ctx.Done():
			return 0, "", ctx.Err()
		}
		if strings.HasSuffix(url, "/api/account") {
			return 200, `{"uuid":"acct` + strings.ReplaceAll(cookies.Signin.Dir, "/", "-") + `"}`, nil
		}
		for org, value := range percent {
			if strings.HasSuffix(url, "/api/organizations/"+org+"/usage") {
				return 200, `{"limits":[{"kind":"session","percent":` + value + `,"resets_at":"` + resets + `"}]}`, nil
			}
		}
		return 404, "{}", nil
	})
	d := newTestDaemon(t, registry, []surface.Surface{claude})
	return pickFixture{t: t, host: host, second: second, handler: dashboardRouter(d), reads: reads, delay: delay, daemon: d}
}

func (f pickFixture) call(method, path string, body any, into any) int {
	f.t.Helper()
	data, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if into != nil {
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			f.t.Fatalf("%s %s: %s", method, path, response.Body.String())
		}
	}
	return response.Code
}

func (f pickFixture) create(configRoot string) (claudepick.Pick, *string) {
	return f.createFrom(configRoot, "")
}

func (f pickFixture) createFrom(configRoot, source string) (claudepick.Pick, *string) {
	f.t.Helper()
	var receipt struct {
		Session surface.Session  `json:"session"`
		Account *claudepick.Pick `json:"account"`
	}
	request := map[string]string{"action": "session-create", "surface": "claude", "message": "hello", "cwd": f.host.Cwd, "configRoot": configRoot, "sourceSessionId": source}
	if code := f.call(http.MethodPost, "/api/v1/actions", request, &receipt); code != http.StatusCreated {
		f.t.Fatalf("create status=%d", code)
	}
	spawns := f.host.Spawns(f.t, receipt.Session.ID)
	if len(spawns) != 1 {
		f.t.Fatalf("spawns=%+v", spawns)
	}
	pick := claudepick.Pick{}
	if receipt.Account != nil {
		pick = *receipt.Account
	}
	return pick, spawns[0].ConfigDir
}

func (f pickFixture) setDefault(name string) int {
	f.t.Helper()
	return f.call(http.MethodPost, "/api/v1/settings", map[string]any{"action": "claude-default-root", "claudeDefaultRoot": name}, nil)
}

func (f pickFixture) claudeSessionIn(id, root string) string {
	f.t.Helper()
	session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transcript: filepath.Join(root, "projects", "-repo", id+".jsonl")}
	if err := f.daemon.Registry.RegisterSession(session); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestAPersonsNewSessionStartsInTheDefaultRootAndNoUsageIsRead(t *testing.T) {
	f := newPickFixture(t, true)
	if code := f.setDefault("nope"); code != http.StatusBadRequest {
		t.Fatalf("an unknown default root is refused: %d", code)
	}
	var preview struct {
		Pick claudepick.Pick `json:"pick"`
	}
	if f.call(http.MethodGet, "/api/v1/session-options/claude-pick", nil, &preview); preview.Pick.Name != "default" || preview.Pick.Reason != "the primary root" {
		t.Fatalf("with no default the primary, even though work has more headroom: %+v", preview.Pick)
	}
	if code := f.setDefault("work"); code != http.StatusOK {
		t.Fatalf("settings status=%d", code)
	}
	var settings struct {
		ClaudeDefaultRoot string `json:"claudeDefaultRoot"`
	}
	if f.call(http.MethodGet, "/api/v1/settings", nil, &settings); settings.ClaudeDefaultRoot != "work" {
		t.Fatalf("settings=%+v", settings)
	}
	pick, dir := f.create("")
	if dir == nil || *dir != f.second || pick.Name != "work" || pick.Account != "work@example.com" || pick.Reason != "your default root for new sessions" {
		t.Fatalf("pick=%+v dir=%v", pick, dir)
	}
	pick, dir = f.create(filepath.Join(f.host.Home, ".claude"))
	if dir != nil || pick.Name != "" {
		t.Fatalf("a named root is always used and reports no choice: pick=%+v dir=%v", pick, dir)
	}
	if n := f.reads.Load(); n != 0 {
		t.Fatalf("placing sessions read claude.ai %d times", n)
	}
}

func TestAnAgentsNewSessionStartsInTheAgentsRootWhateverTheDefault(t *testing.T) {
	f := newPickFixture(t, true)
	if code := f.setDefault("default"); code != http.StatusOK {
		t.Fatalf("settings status=%d", code)
	}
	agent := f.claudeSessionIn("agent-in-work", f.second)
	pick, dir := f.createFrom("", agent)
	if dir == nil || *dir != f.second || pick.Name != "work" || pick.Reason != "the root of the session that created it" {
		t.Fatalf("pick=%+v dir=%v", pick, dir)
	}
	if n := f.reads.Load(); n != 0 {
		t.Fatalf("placing sessions read claude.ai %d times", n)
	}
}

func TestAReplacementLeadStartsInTheReplacedLeadsRoot(t *testing.T) {
	f := newPickFixture(t, true)
	if code := f.setDefault("default"); code != http.StatusOK {
		t.Fatalf("settings status=%d", code)
	}
	root := f.daemon.coordinator().ClaudeRoot
	if got, err := root(f.claudeSessionIn("old-lead", f.second)); err != nil || got != f.second {
		t.Fatalf("a replacement goes where the old lead lived, not the default: %q err=%v", got, err)
	}
	if got, err := root(""); err != nil || got != "" {
		t.Fatalf("a first lead starts in the default root, here the primary: %q err=%v", got, err)
	}
	if n := f.reads.Load(); n != 0 {
		t.Fatalf("placing a lead read claude.ai %d times", n)
	}
}

func TestNoSessionStartsWhenTheDefaultRootCannotBeRead(t *testing.T) {
	f := newPickFixture(t, true)
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): `{"codexRecentHours":9999,"claude":{"defaultRoot":"work"}}`})
	request := map[string]string{"action": "session-create", "surface": "claude", "message": "hello", "cwd": f.host.Cwd}
	if code := f.call(http.MethodPost, "/api/v1/actions", request, nil); code != http.StatusInternalServerError {
		t.Fatalf("a create that cannot read the default must refuse, not start in the primary: %d", code)
	}
	if code := f.call(http.MethodGet, "/api/v1/session-options/claude-pick", nil, nil); code != http.StatusInternalServerError {
		t.Fatalf("the preview must not claim the primary: %d", code)
	}
	if _, err := f.daemon.coordinator().ClaudeRoot(""); err == nil {
		t.Fatal("a lead start must not fall back to the primary")
	}
	sessions, err := f.daemon.Registry.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.Surface == surface.KindClaude {
			t.Fatalf("a Claude session started anyway: %s", session.ID)
		}
	}
}
