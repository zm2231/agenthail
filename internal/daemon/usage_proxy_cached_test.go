package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
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

func TestTheDaemonStartsTheFirstProxyReadWhenItStarts(t *testing.T) {
	f := newProxyUsageFixture(t, "file")
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = d.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
		store.Close()
	})
	configs, err := surface.ConfiguredProxies(f.home)
	if err != nil || len(configs) != 1 {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
	entry := d.proxyUsage.entry(configs[0])
	deadline := time.Now().Add(10 * time.Second)
	for {
		if last := entry.last.Load(); last != nil && last.err == nil && len(last.accounts) == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no reading landed after the daemon started, with nothing asking for usage")
		}
		time.Sleep(20 * time.Millisecond)
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
