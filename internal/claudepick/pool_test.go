package claudepick

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func capped(percent, ceiling float64, resetsIn time.Duration) *Window {
	return &Window{Percent: percent, ResetsAt: now.Add(resetsIn), Ceiling: ceiling}
}

func viaProxy(pool ...Member) Candidate {
	return Candidate{Name: "work", Root: "/home/dev/.work", Account: "stale@example.com", Login: surface.ClaudeLoginProxy, Pool: pool}
}

func TestAProxyRootCompetesWithItsFreestAccount(t *testing.T) {
	proxy := viaProxy(
		Member{Account: "ops@example.com", Session: capped(50, 60, time.Hour), Weekly: capped(10, 60, 48*time.Hour)},
		Member{Account: "lead@example.com", Session: capped(6, 60, time.Hour), Weekly: capped(9, 60, 48*time.Hour)},
		Member{Account: "spare@example.com", Session: capped(1, 60, time.Hour), RetryAt: now.Add(time.Hour)},
	)
	got := Choose(append(roots(), proxy), DefaultPolicy(), now)
	if got.Root != "/home/dev/.work" || got.Account != "lead@example.com" || got.Reason != "most headroom, best: lead@example.com, session 6%, week 9%" {
		t.Fatalf("the proxy root is eligible with its freest open account, never a cooling one: %+v", got)
	}
}

func TestAProxyAccountIsJudgedByTheProxysOwnCeiling(t *testing.T) {
	proxy := viaProxy(Member{Account: "ops@example.com", Session: capped(65, 60, 30*time.Minute), Weekly: capped(10, 60, 48*time.Hour)})
	candidates := []Candidate{roots()[0], proxy}
	if got := Choose(candidates, DefaultPolicy(), now); got.Name != "default" {
		t.Fatalf("65%% is over the proxy's 60%% ceiling though under Agenthail's 90%%: %+v", got)
	}
}

func TestAProxyWithoutReadingsRanksAsUnknown(t *testing.T) {
	got := Choose(append(roots(), viaProxy()), DefaultPolicy(), now)
	if got.Name != "team" {
		t.Fatalf("%+v", got)
	}
	candidates := []Candidate{{Name: "default", Root: "/home/dev/.claude", Login: surface.ClaudeLoginAPIKey, Primary: true}, viaProxy()}
	if got := Choose(candidates, DefaultPolicy(), now); got.Name != "work" || got.Reason != "no usage reading" {
		t.Fatalf("an unread proxy is still a subscription login: %+v", got)
	}
}

func TestAPreferredProxySticksEvenWhenEveryAccountIsFull(t *testing.T) {
	policy := DefaultPolicy()
	policy.Preferred = "work"
	proxy := viaProxy(
		Member{Account: "ops@example.com", Session: capped(70, 60, 3*time.Hour)},
		Member{Account: "lead@example.com", Session: capped(10, 60, time.Hour), RetryAt: now.Add(2 * time.Hour)},
	)
	got := Choose(append(roots(), proxy), policy, now)
	if got.Name != "work" || got.Reason != "preferred proxy; every account is near its limit, the proxy will wait for the first to free up in 2h 0m" {
		t.Fatalf("leaving a preferred proxy would break its single egress: %+v", got)
	}
	open := viaProxy(Member{Account: "ops@example.com", Session: capped(20, 60, time.Hour)})
	if got := Choose(append(roots(), open), policy, now); got.Reason != "preferred, best: ops@example.com, session 20%" {
		t.Fatalf("%+v", got)
	}
	if got := Choose(append(roots(), viaProxy()), policy, now); got.Name != "work" || got.Reason != "preferred; the proxy chooses the account" {
		t.Fatalf("%+v", got)
	}
}

func TestAFullProxyCountsAsFullAmongOthers(t *testing.T) {
	policy := DefaultPolicy()
	policy.SessionCeiling = 10
	proxy := viaProxy(Member{Account: "ops@example.com", Session: capped(70, 60, 20*time.Minute)})
	got := Choose(append(roots(), proxy), policy, now)
	if got.Name != "work" || !strings.Contains(got.Reason, "frees up first, in 20m") {
		t.Fatalf("when every root is full the proxy competes on when its first account frees up: %+v", got)
	}
}
