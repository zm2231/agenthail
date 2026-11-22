package daemon

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) readProxyAccounts(ctx context.Context, name string, now time.Time) ([]proxyacct.Account, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false
	}
	configs, err := surface.ConfiguredProxies(home)
	if err != nil {
		return nil, false
	}
	for _, config := range configs {
		if config.Name != name {
			continue
		}
		if reading := d.proxyUsage.Read(ctx, config, now); reading.Err == nil {
			return reading.Accounts, true
		}
		return nil, false
	}
	return nil, false
}

func (d *Daemon) proxyRootUsage(ctx context.Context, root surface.ClaudeConfigRoot, now time.Time) map[string]any {
	answer := map[string]any{"root": root.Root, "proxy": root.Proxy}
	accounts, ok := d.readProxyAccounts(ctx, root.Proxy, now)
	if !ok {
		answer["usageProblem"] = "the " + root.Proxy + " proxy's accounts are not readable from this host"
		return answer
	}
	route := proxyacct.Routing(accounts, now, false)
	id := route.Routed["claude"]
	if route.Full {
		id = proxyFirstFree(accounts, route, now)
	}
	var next *proxyacct.Account
	for i := range accounts {
		if accounts[i].ID == id && accounts[i].Provider == "claude" {
			next = &accounts[i]
		}
	}
	if next == nil {
		answer["usageProblem"] = "the " + root.Proxy + " proxy reports no Claude account"
		return answer
	}
	reading := &surface.Usage{FetchedAt: now}
	for _, window := range next.Windows {
		if !window.ResetsAt.IsZero() && !now.Before(window.ResetsAt) {
			continue
		}
		switch window.Kind {
		case proxyacct.WindowSession:
			reading.Limits = append(reading.Limits, surface.UsageWindow{Kind: "session", Percent: window.Percent, ResetsAt: window.ResetsAt})
		case proxyacct.WindowWeek:
			reading.Limits = append(reading.Limits, surface.UsageWindow{Kind: "weekly_all", Percent: window.Percent, ResetsAt: window.ResetsAt})
		}
	}
	answer["usage"], answer["best"] = reading, next.Label
	if route.Full {
		answer["full"] = true
		answer["usageProblem"] = "every " + root.Proxy + " account is near its limit"
		if route.FreesAt.After(now) {
			answer["fullUntil"] = route.FreesAt
			answer["usageProblem"] = "every " + root.Proxy + " account is near its limit; the first frees up in " + usage.ProxyWait(route.FreesAt.Sub(now))
		}
	}
	return answer
}

func proxyFirstFree(accounts []proxyacct.Account, route proxyacct.Route, now time.Time) string {
	first, at := route.Lead, time.Time{}
	for _, account := range accounts {
		if account.Provider != "claude" || account.Status == proxyacct.StatusDisabled || account.Status == proxyacct.StatusUnavailable {
			continue
		}
		if frees, _ := proxyacct.Full([]proxyacct.Account{account}, now, false); !frees.IsZero() && (at.IsZero() || frees.Before(at)) {
			first, at = account.ID, frees
		}
	}
	return first
}
