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
	f.t.Helper()
	var receipt struct {
		Session surface.Session  `json:"session"`
		Account *claudepick.Pick `json:"account"`
	}
	request := map[string]string{"action": "session-create", "surface": "claude", "message": "hello", "cwd": f.host.Cwd, "configRoot": configRoot}
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

func TestANewClaudeSessionStartsInTheAccountWithMostHeadroom(t *testing.T) {
	f := newPickFixture(t, true)
	var preview struct {
		Pick   claudepick.Pick   `json:"pick"`
		Policy claudepick.Policy `json:"policy"`
	}
	if code := f.call(http.MethodGet, "/api/v1/session-options/claude-pick", nil, &preview); code != http.StatusOK || preview.Pick.Name != "work" || preview.Pick.Reason != "most headroom, session 10%" || !preview.Policy.Auto || preview.Policy.SessionCeiling != 90 {
		t.Fatalf("preview=%+v", preview)
	}
	pick, dir := f.create("")
	if dir == nil || *dir != f.second || pick.Name != "work" || pick.Account != "work@example.com" {
		t.Fatalf("an unnamed root follows the policy: pick=%+v dir=%v", pick, dir)
	}
	pick, dir = f.create(filepath.Join(f.host.Home, ".claude"))
	if dir != nil || pick.Name != "" {
		t.Fatalf("a named root is always used and reports no choice: pick=%+v dir=%v", pick, dir)
	}
}

func TestAPreferredRootIsChosenWhileUnderItsCeilings(t *testing.T) {
	f := newPickFixture(t, true)
	policy := claudepick.Policy{Auto: true, Preferred: "nope", SessionCeiling: 90, WeeklyCeiling: 90}
	if code := f.call(http.MethodPost, "/api/v1/settings", map[string]any{"action": "claude-accounts", "claudeAccounts": policy}, nil); code != http.StatusBadRequest {
		t.Fatalf("an unknown preferred root is refused: %d", code)
	}
	policy.Preferred = "default"
	if code := f.call(http.MethodPost, "/api/v1/settings", map[string]any{"action": "claude-accounts", "claudeAccounts": policy}, nil); code != http.StatusOK {
		t.Fatalf("settings status=%d", code)
	}
	var settings struct {
		ClaudeAccounts claudepick.Policy `json:"claudeAccounts"`
	}
	if f.call(http.MethodGet, "/api/v1/settings", nil, &settings); settings.ClaudeAccounts != policy {
		t.Fatalf("settings=%+v", settings.ClaudeAccounts)
	}
	f.call(http.MethodGet, "/api/v1/session-options/claude-pick", nil, nil)
	pick, dir := f.create("")
	if dir != nil || pick.Name != "default" || pick.Reason != "preferred, session 80%" {
		t.Fatalf("pick=%+v dir=%v", pick, dir)
	}
}

func TestAReadingNeverFollowsARootToItsNextLogin(t *testing.T) {
	f := newPickFixture(t, true)
	f.call(http.MethodGet, "/api/v1/session-options/claude-pick", nil, nil)
	writeUsageFixture(t, map[string]string{filepath.Join(f.second, ".claude.json"): `{"oauthAccount":{"accountUuid":"acct-c","emailAddress":"new@example.com","organizationUuid":"org-c"}}`})
	if pick, dir := f.create(""); dir != nil || pick.Name != "default" || pick.Reason != "most headroom, session 80%" {
		t.Fatalf("the new login starts unknown, not with the old login's 10%%: pick=%+v dir=%v", pick, dir)
	}
}

func TestACreateNeverWaitsOnClaudeAIForUsage(t *testing.T) {
	f := newPickFixture(t, true)
	f.delay.Store(int64(3 * time.Second))
	started := time.Now()
	pick, dir := f.create("")
	if took := time.Since(started); took > time.Second || dir != nil || pick.Reason != "no usage reading" {
		t.Fatalf("a cold cache starts in the primary at once: took=%v pick=%+v dir=%v", took, pick, dir)
	}
	f.delay.Store(0)
	deadline := time.Now().Add(10 * time.Second)
	for f.daemon.claudeUsage.entry(f.second).last.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the stale reading was not refreshed in the background")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pick, dir := f.create(""); dir == nil || *dir != f.second || pick.Reason != "most headroom, session 10%" {
		t.Fatalf("the refreshed readings decide the next start: pick=%+v dir=%v", pick, dir)
	}
}

func TestASingleRootStartsAsItAlwaysHas(t *testing.T) {
	f := newPickFixture(t, false)
	pick, dir := f.create("")
	if dir != nil || pick.Name != "default" || pick.Account != "me@example.com" || pick.Reason != "the only account" {
		t.Fatalf("pick=%+v dir=%v", pick, dir)
	}
	if n := f.reads.Load(); n != 0 {
		t.Fatalf("a single root waited on %d claude.ai reads before starting", n)
	}
}
