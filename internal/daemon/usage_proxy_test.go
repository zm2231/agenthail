package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type proxyUsageFixture struct {
	daemon *Daemon
	proxy  *proxyaccttest.Proxy
	home   string
	team   string
	port   string
}

func newProxyUsageFixture(t *testing.T, management string) proxyUsageFixture {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	proxy := proxyaccttest.New(t)
	now := time.Now().UTC()
	proxy.Files.Store(fmt.Sprintf(`{"files":[
 {"name":"claude-dev.json","provider":"claude","email":"dev@example.com","status":"active","quota_ceiling":{"5h":60,"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":"0.18","Anthropic-Ratelimit-Unified-5h-Reset":"%d","Anthropic-Ratelimit-Unified-7d-Utilization":"0.65","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}},
 {"name":"claude-team.json","provider":"claude","email":"team@example.com","status":"active","disabled":true}]}`,
		now.Add(-2*time.Minute).Format(time.RFC3339), now.Add(3*time.Hour).Unix(), now.Add(50*time.Hour).Unix()))
	port := proxy.Server.URL[strings.LastIndex(proxy.Server.URL, ":")+1:]
	team := filepath.Join(home, ".team", ".claude")
	if management == "file" {
		management = `,"management":{"file":"` + proxyaccttest.KeyFile(t, home, proxyaccttest.Key, 0o600) + `"}`
	}
	writeUsageFixture(t, map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"claudeConfigDirs":["` + team + `"],"proxies":[{"name":"shared","url":"` + proxy.Server.URL + `"` + management + `}]}`,
		filepath.Join(home, ".claude.json"):              `{"oauthAccount":{"accountUuid":"acct-work","emailAddress":"work@example.com","organizationUuid":"org-work"}}`,
		filepath.Join(team, "settings.json"):             `{"env":{"ANTHROPIC_BASE_URL":"http://localhost:` + port + `/","ANTHROPIC_AUTH_TOKEN":"client-token"}}`,
	})
	d.Surfaces = []surface.Surface{surfaces.NewClaudeWithRequest(candidates(nil), home, func(context.Context, string, string, map[string]string, string, surfaces.CookieSource, time.Duration) (int, string, error) {
		return 404, "{}", nil
	})}
	return proxyUsageFixture{daemon: d, proxy: proxy, home: home, team: team, port: port}
}

func (f proxyUsageFixture) usage(t *testing.T) (usageBody, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage?scope=local", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	f.daemon.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	raw, _ := io.ReadAll(response.Body)
	var body usageBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return body, string(raw)
}

func TestUsageShowsAProxysAccountsAndTheRootsThatUseIt(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	body, raw := f.usage(t)
	if strings.Contains(raw, proxyaccttest.Key) || strings.Contains(raw, "client-token") {
		t.Fatal("a key reached the usage answer")
	}
	if len(body.Sets) != 3 {
		t.Fatalf("sets=%+v", body.Sets)
	}
	team, group := body.Sets[1], body.Sets[2]
	wantID := "http://loopback:" + f.port + "@" + f.daemon.localHost().ID
	if team.Root != f.team || team.Login != surface.ClaudeLoginProxy || team.Proxy == nil || team.Proxy.Name != "shared" || team.Proxy.ID != wantID || team.Problem != "" {
		t.Fatalf("a root whose base URL is the proxy's is via that proxy: %+v proxy=%+v", team, team.Proxy)
	}
	if group.Provider != "proxy" || group.Name != "shared" || group.Proxy.ID != wantID || !strings.HasPrefix(group.Problem, "Every shared Claude account is over its ceiling; the first frees up in 2d 2h.") || len(group.Accounts) != 2 {
		t.Fatalf("group=%+v", group)
	}
	dev, old := group.Accounts[0], group.Accounts[1]
	if dev.Label != "dev@example.com" || len(dev.Meters) != 2 || dev.Meters[0].Label != "Claude" || dev.Meters[0].Percent != 18 || dev.Meters[0].Ceiling != 60 || dev.Meters[1].Severity != "critical" {
		t.Fatalf("each account has meters with the proxy's ceiling: %+v", dev)
	}
	if old.Status != proxyacct.StatusDisabled {
		t.Fatalf("old=%+v", old)
	}
	f.usage(t)
	if n := f.proxy.Attempts.Load(); n != 1 {
		t.Fatalf("the proxy is read once a minute however many polls ask: %d", n)
	}
}

func TestAHostWithoutAManagementKeyStillShowsItsRootsViaTheProxy(t *testing.T) {
	f := newProxyUsageFixture(t, "")
	body, _ := f.usage(t)
	team, group := body.Sets[1], body.Sets[2]
	if team.Login != surface.ClaudeLoginProxy || group.ProxyProblem != string(proxyacct.KeyMissing) || len(group.Accounts) != 0 {
		t.Fatalf("team=%+v group=%+v", team, group)
	}
	if !strings.Contains(group.Problem, "Accounts not readable from any host") || !strings.Contains(group.Detail, "agenthail proxies key shared") {
		t.Fatalf("group=%+v", group)
	}
	if n := f.proxy.Attempts.Load(); n != 0 {
		t.Fatalf("management was called with no key: %d", n)
	}
}

func TestARefusedKeySaysHowToFixIt(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	f.proxy.Forced.Store(`401 {"error":"invalid management key"}`)
	body, raw := f.usage(t)
	group := body.Sets[2]
	if group.ProxyProblem != string(proxyacct.KeyRefused) || !strings.Contains(group.Problem, "the key file "+filepath.Join(f.home, "proxy.key")) || !strings.Contains(group.Detail, "agenthail proxies key shared --key-file") || strings.Contains(raw, proxyaccttest.Key) {
		t.Fatalf("group=%+v", group)
	}
}

func TestASlowKeyCommandGetsItsWholeTimeToRead(t *testing.T) {
	if testing.Short() {
		t.Skip("waits nine seconds for the key")
	}
	slow := `sleep 9; printf '%s\n' "$0"`
	f := newProxyUsageFixture(t, `,"management":{"command":["/bin/sh","-c","`+strings.ReplaceAll(slow, `"`, `\"`)+`","`+proxyaccttest.Key+`"]}`)
	body, raw := f.usage(t)
	group := body.Sets[2]
	if group.ProxyProblem != "" || len(group.Accounts) != 2 || strings.Contains(raw, proxyaccttest.Key) {
		t.Fatalf("group=%+v", group)
	}
}

func TestAProxySharedByHostsShowsOnceFromAHostThatCanReadIt(t *testing.T) {
	shared := &usageProxy{Name: "shared", ID: "http://proxy.example:8317"}
	sets := mergeProxySets([]usageSet{
		{Host: "laptop", Provider: "claude", Name: "default"},
		{Host: "laptop", Provider: "proxy", Name: "shared", Proxy: shared, ProxyProblem: string(proxyacct.KeyMissing), Problem: "no key"},
		{Host: "server", Provider: "proxy", Name: "shared", Proxy: shared, Accounts: []usageAccount{{Label: "dev@example.com"}}},
		{Host: "server", Provider: "proxy", Name: "local", Proxy: &usageProxy{Name: "local", ID: "http://loopback:8317@server"}, ProxyProblem: string(proxyacct.KeyMissing)},
		{Host: "other", Provider: "proxy", Name: "local", Proxy: &usageProxy{Name: "local", ID: "http://loopback:8317@other"}, ProxyProblem: string(proxyacct.KeyMissing)},
	})
	if len(sets) != 4 || sets[1].Host != "server" || len(sets[1].Accounts) != 1 || sets[1].Problem != "" {
		t.Fatalf("sets=%+v", sets)
	}
	if !strings.Contains(sets[2].Problem, "no host has a management key") || sets[3].Host != "other" {
		t.Fatalf("a proxy on each host's own loopback is its own: %+v", sets[2:])
	}
}
