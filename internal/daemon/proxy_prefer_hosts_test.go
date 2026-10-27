package daemon

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAPreferenceChangeRunsOnTheHostThatReadsTheProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	proxy := proxyaccttest.New(t)
	proxy.Files.Store(preferFiles)
	writeUsageFixture(t, map[string]string{
		filepath.Join(home, ".agenthail", "config.json"): `{"proxies":[{"name":"shared","url":"` + proxy.Server.URL + `","management":{"file":"` + proxyaccttest.KeyFile(t, home, proxyaccttest.Key, 0o600) + `"}}]}`,
	})
	laptop := newFederationNode(t, "laptop-a")
	vps := newFederationNode(t, "vps-a")
	laptop.serve(t, nil)
	vps.serve(t, nil)
	roots := testRoots(laptop.server)
	laptop.start(t, roots)
	vps.start(t, roots)
	record := laptop.pair(t, vps, "vps")
	testkit.Eventually(t, "laptop mirrors vps", 10*time.Second, func() bool {
		return len(sessionsOf(laptop.snapshot(t), record.ID)) == 1
	})
	var usage usageBody
	if err := json.Unmarshal(vps.request(t, http.MethodGet, "/api/v1/usage?scope=local", nil).Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, set := range usage.Sets {
		if set.Provider == "proxy" && set.Proxy != nil {
			id = set.Proxy.ID
		}
	}
	query := url.Values{"proxy": {id}, "account": {"claude-spare.json"}, "set": {preferOn}}
	preview := laptop.request(t, http.MethodGet, "/api/v1/proxies/prefer?"+query.Encode(), nil)
	var plan proxyPreferPlan
	if preview.Code != http.StatusOK || json.Unmarshal(preview.Body.Bytes(), &plan) != nil || plan.Host != vps.daemon.localHost().Name || len(plan.Changes) != 1 {
		t.Fatalf("the preview comes from the host that reads the proxy: %d %s", preview.Code, preview.Body)
	}
	applied := laptop.request(t, http.MethodPost, "/api/v1/proxies/prefer", proxyPreferRequest{Proxy: id, Account: "claude-spare.json", Set: preferOn, PlanID: plan.PlanID})
	if applied.Code != http.StatusOK {
		t.Fatalf("%d %s", applied.Code, applied.Body)
	}
	if writes := proxy.Writes(); len(writes) != 1 {
		t.Fatalf("writes=%q", writes)
	}
}
