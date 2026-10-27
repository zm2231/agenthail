package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f proxyUsageFixture) get(t *testing.T, path string, into any) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	f.daemon.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
		t.Fatalf("%s: %s", path, response.Body.String())
	}
	return response.Body.String()
}

func (f proxyUsageFixture) pick(t *testing.T) claudepick.Pick {
	var answer struct {
		Pick claudepick.Pick `json:"pick"`
	}
	f.get(t, "/api/v1/session-options/claude-pick", &answer)
	return answer.Pick
}

func TestAProxyRootIsPickedWithItsFreestAccount(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	now := time.Now().UTC()
	f.proxy.Files.Store(fmt.Sprintf(`{"files":[
 {"name":"claude-ops.json","provider":"claude","email":"ops@example.com","status":"active","quota_ceiling":{"5h":60,"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":"0.12","Anthropic-Ratelimit-Unified-5h-Reset":"%d","Anthropic-Ratelimit-Unified-7d-Utilization":"0.30","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}}]}`,
		now.Format(time.RFC3339), now.Add(3*time.Hour).Unix(), now.Add(50*time.Hour).Unix()))
	if got := f.pick(t); got.Name != "team" || got.Account != "ops@example.com" || got.Reason != "most headroom, next: ops@example.com, session 12%, week 30%" {
		t.Fatalf("a root through the proxy competes with its freest account: %+v", got)
	}
	var usage struct {
		Best  string `json:"best"`
		Usage struct {
			Limits []struct {
				Kind    string  `json:"kind"`
				Percent float64 `json:"percent"`
			} `json:"limits"`
		} `json:"usage"`
		Problem string `json:"usageProblem"`
	}
	raw := f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "ops@example.com" || len(usage.Usage.Limits) != 2 || usage.Usage.Limits[0].Kind != "session" || usage.Usage.Limits[0].Percent != 12 || usage.Problem != "" {
		t.Fatalf("the picker reads a proxy root as its best account: %s", raw)
	}
	if strings.Contains(raw, proxyaccttest.Key) {
		t.Fatal("the key reached the answer")
	}
}

func TestAPreferredProxyRootSticksWhenEveryAccountIsFull(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): `{"claudeAccounts":{"auto":true,"preferred":"team","sessionCeiling":90,"weeklyCeiling":90}}`})
	got := f.pick(t)
	if got.Name != "team" || !strings.HasPrefix(got.Reason, "preferred proxy; every account is near its limit, the proxy will wait for the first to free up in 2d") {
		t.Fatalf("a preferred proxy is kept: leaving it breaks its single egress: %+v", got)
	}
	var usage struct {
		Best    string `json:"best"`
		Problem string `json:"usageProblem"`
	}
	f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "dev@example.com" || !strings.HasPrefix(usage.Problem, "every shared account is near its limit; the first frees up in 2d") {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestASessionStartNeverWaitsOnTheProxy(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	configs, err := surface.ConfiguredProxies(f.home)
	if err != nil || len(configs) != 1 {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
	if reading := f.daemon.proxyUsage.cached(configs[0], time.Now()); reading != nil {
		t.Fatalf("with no reading yet the start goes on without one: %+v", reading)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.daemon.proxyUsage.cached(configs[0], time.Now()) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	reading := f.daemon.proxyUsage.cached(configs[0], time.Now())
	if reading == nil || reading.err != nil || len(reading.accounts) != 2 {
		t.Fatalf("the background read fills the next start: %+v", reading)
	}
	if attempts := f.proxy.Attempts.Load(); attempts != 1 {
		t.Fatalf("repeated starts within a minute read the proxy once: %d", attempts)
	}
}

func TestAProxyRootWithoutAReadableKeyRanksAsUnknown(t *testing.T) {
	f := newProxyUsageFixture(t, "")
	var usage struct {
		Best    string `json:"best"`
		Problem string `json:"usageProblem"`
	}
	f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "" || usage.Problem != "the shared proxy's accounts are not readable from this host" {
		t.Fatalf("usage=%+v", usage)
	}
	if got := f.pick(t); got.Name != "default" || got.Reason != "no usage reading" {
		t.Fatalf("with no reading anywhere the primary starts, as for direct roots: %+v", got)
	}
}
