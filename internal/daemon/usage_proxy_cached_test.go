package daemon

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

func TestAUsageAnswerNeverWaitsOnTheProxy(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	f.proxy.SlowHealth.Store(int64(2 * time.Second))
	started := time.Now()
	body, _ := f.usageOnce(t)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the first answer waited %s for the proxy", elapsed)
	}
	if group := body.Sets[2]; group.ProxyProblem != proxyReadingProblem || group.Problem != "Reading the shared proxy's accounts." {
		t.Fatalf("group=%+v", group)
	}
	body, _ = f.usage(t)
	if group := body.Sets[2]; group.ProxyProblem != "" || len(group.Accounts) != 2 {
		t.Fatalf("the background read lands: %+v", group)
	}
	started = time.Now()
	sets := f.daemon.proxyUsageSets(time.Now().Add(2 * proxyUsageTTL))
	if elapsed := time.Since(started); elapsed > time.Second || len(sets) != 1 || len(sets[0].Accounts) != 2 {
		t.Fatalf("an old reading is served while it refreshes, after %s: %+v", elapsed, sets)
	}
}

func TestAHostReadingTheProxyOutranksOneWithoutAKey(t *testing.T) {
	shared := &usageProxy{Name: "shared", ID: "http://proxy.example:8317"}
	sets := mergeProxySets([]usageSet{
		{Host: "laptop", Provider: "proxy", Name: "shared", Proxy: shared, ProxyProblem: string(proxyacct.KeyMissing), Problem: "no key"},
		{Host: "server", Provider: "proxy", Name: "shared", Proxy: shared, ProxyProblem: proxyReadingProblem, Problem: "Reading the shared proxy's accounts."},
	})
	if len(sets) != 1 || sets[0].Host != "server" || sets[0].Problem != "Reading the shared proxy's accounts." {
		t.Fatalf("sets=%+v", sets)
	}
}

func TestAPlanIsMadeWhileTheFirstReadIsUnderWay(t *testing.T) {
	f, proxy := newPreferFixture(t)
	f.daemon.proxyUsage.retain(nil)
	f.proxy.SlowHealth.Store(int64(300 * time.Millisecond))
	plan := f.plan(t, proxy, "claude-spare.json", preferOn, false)
	if plan.PlanID == "" || len(plan.Changes) != 1 {
		t.Fatalf("plan=%+v", plan)
	}
	if code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOn, PlanID: plan.PlanID}); code != http.StatusOK || !strings.Contains(body, "spare@example.com set to 10") {
		t.Fatalf("%d %s", code, body)
	}
}
