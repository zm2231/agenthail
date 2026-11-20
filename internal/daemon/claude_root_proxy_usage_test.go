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

func TestAProxyRootReadsAsItsFreestAccount(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	now := time.Now().UTC()
	f.proxy.Files.Store(fmt.Sprintf(`{"files":[
 {"name":"claude-ops.json","provider":"claude","email":"ops@example.com","status":"active","quota_ceiling":{"5h":60,"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":"0.12","Anthropic-Ratelimit-Unified-5h-Reset":"%d","Anthropic-Ratelimit-Unified-7d-Utilization":"0.30","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}}]}`,
		now.Format(time.RFC3339), now.Add(3*time.Hour).Unix(), now.Add(50*time.Hour).Unix()))
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

func TestAProxyRootReadsAsTheAccountTheProxyRoutesTo(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	now := time.Now().UTC()
	signals := func(session, week string) string {
		return fmt.Sprintf(`{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":%q,"Anthropic-Ratelimit-Unified-5h-Reset":"%d","Anthropic-Ratelimit-Unified-7d-Utilization":%q,"Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}`,
			now.Format(time.RFC3339), session, now.Add(3*time.Hour).Unix(), week, now.Add(50*time.Hour).Unix())
	}
	f.proxy.Files.Store(`{"files":[
 {"name":"claude-main.json","provider":"claude","email":"main@example.com","status":"active","quota_preference":10,"quota":` + signals("0.40", "0.45") + `},
 {"name":"claude-spare.json","provider":"claude","email":"spare@example.com","status":"active","quota_preference":0,"quota":` + signals("0.02", "0.05") + `},
 {"name":"claude-team.json","provider":"claude","email":"team@example.com","status":"active","next_retry_after":"2099-01-01T00:00:00Z","quota_preference":10}]}`)
	var usage struct {
		Best    string `json:"best"`
		Problem string `json:"usageProblem"`
	}
	raw := f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "main@example.com" || usage.Problem != "" {
		t.Fatalf("the picker names the preferred account the proxy sends work to, not a freer one it would not use: %s", raw)
	}
}

func TestAFullProxyRootReadsAsTheAccountThatFreesFirst(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	now := time.Now().UTC()
	f.proxy.Files.Store(fmt.Sprintf(`{"files":[
 {"name":"claude-main.json","provider":"claude","email":"main@example.com","status":"active","quota_ceiling":{"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-7d-Utilization":"0.62","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}},
 {"name":"claude-team.json","provider":"claude","email":"team@example.com","status":"active","next_retry_after":%q,
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-7d-Utilization":"1.0","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}}]}`,
		now.Format(time.RFC3339), now.Add(5*time.Hour).Unix(), now.Add(30*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339), now.Add(30*time.Hour).Unix()))
	var usage struct {
		Best      string    `json:"best"`
		Problem   string    `json:"usageProblem"`
		Full      bool      `json:"full"`
		FullUntil time.Time `json:"fullUntil"`
	}
	raw := f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "main@example.com" || !strings.Contains(usage.Problem, "the first frees up in 5h") || !usage.Full || usage.FullUntil.Unix() != now.Add(5*time.Hour).Unix() {
		t.Fatalf("a full root says when the pool frees up and reads as the account that frees first: %s", raw)
	}
}

func TestAFullProxyRootGivesNoTimeWhenAFullAccountDoesNotSayWhen(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	now := time.Now().UTC()
	f.proxy.Files.Store(fmt.Sprintf(`{"files":[
 {"name":"claude-main.json","provider":"claude","email":"main@example.com","status":"active","quota_ceiling":{"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-7d-Utilization":"0.62"}}},
 {"name":"claude-team.json","provider":"claude","email":"team@example.com","status":"active","quota_ceiling":{"7d":60},
  "quota":{"observed_at":%q,"signals":{"Anthropic-Ratelimit-Unified-7d-Utilization":"1.0","Anthropic-Ratelimit-Unified-7d-Reset":"%d"}}}]}`,
		now.Format(time.RFC3339), now.Format(time.RFC3339), now.Add(5*time.Hour).Unix()))
	var usage struct {
		Problem   string    `json:"usageProblem"`
		Full      bool      `json:"full"`
		FullUntil time.Time `json:"fullUntil"`
	}
	raw := f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if !usage.Full || !usage.FullUntil.IsZero() || strings.Contains(usage.Problem, "frees up") {
		t.Fatalf("when one full account does not say when it frees, the pool gives no time: %s", raw)
	}
}

func TestAFullProxyRootSaysWhenItsFirstAccountFreesUp(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	var usage struct {
		Best    string `json:"best"`
		Problem string `json:"usageProblem"`
	}
	f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "dev@example.com" || !strings.HasPrefix(usage.Problem, "every shared account is near its limit; the first frees up in 2d") {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestACachedProxyReadNeverWaitsOnTheProxy(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	configs, err := surface.ConfiguredProxies(f.home)
	if err != nil || len(configs) != 1 {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
	if reading := f.daemon.proxyUsage.cached(configs[0], time.Now()); reading != nil {
		t.Fatalf("with no reading yet the reader goes on without one: %+v", reading)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.daemon.proxyUsage.cached(configs[0], time.Now()) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	reading := f.daemon.proxyUsage.cached(configs[0], time.Now())
	if reading == nil || reading.err != nil || len(reading.accounts) != 2 {
		t.Fatalf("the background read fills the next reader: %+v", reading)
	}
	if attempts := f.proxy.Attempts.Load(); attempts != 1 {
		t.Fatalf("repeated reads within a minute read the proxy once: %d", attempts)
	}
}

func TestAProxyRootWithoutAReadableKeySaysSo(t *testing.T) {
	f := newProxyUsageFixture(t, "")
	var usage struct {
		Best    string `json:"best"`
		Problem string `json:"usageProblem"`
	}
	f.get(t, "/api/v1/session-options/claude-usage?root="+url.QueryEscape(f.team), &usage)
	if usage.Best != "" || usage.Problem != "the shared proxy's accounts are not readable from this host" {
		t.Fatalf("usage=%+v", usage)
	}
}
