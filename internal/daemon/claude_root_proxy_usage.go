package daemon

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) readProxyPool(ctx context.Context, name string, now time.Time) ([]claudepick.Member, bool) {
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
		if reading := d.proxyUsage.read(ctx, config, now); reading.err == nil {
			return proxyPool(reading.accounts, now), true
		}
		return nil, false
	}
	return nil, false
}

func (d *Daemon) proxyRootUsage(ctx context.Context, root surface.ClaudeConfigRoot, now time.Time) map[string]any {
	answer := map[string]any{"root": root.Root, "proxy": root.Proxy}
	pool, ok := d.readProxyPool(ctx, root.Proxy, now)
	if !ok {
		answer["usageProblem"] = "the " + root.Proxy + " proxy's accounts are not readable from this host"
		return answer
	}
	best := claudepick.Pooled(pool, now)
	if best.Best == "" {
		answer["usageProblem"] = "the " + root.Proxy + " proxy reports no Claude account"
		return answer
	}
	usage := &surface.Usage{FetchedAt: now}
	if best.Session != nil {
		usage.Limits = append(usage.Limits, surface.UsageWindow{Kind: "session", Percent: best.Session.Percent, ResetsAt: best.Session.ResetsAt})
	}
	if best.Weekly != nil {
		usage.Limits = append(usage.Limits, surface.UsageWindow{Kind: "weekly_all", Percent: best.Weekly.Percent, ResetsAt: best.Weekly.ResetsAt})
	}
	answer["usage"], answer["best"] = usage, best.Best
	if !best.FullUntil.IsZero() {
		answer["usageProblem"] = "every " + root.Proxy + " account is near its limit; the first frees up in " + proxyWait(best.FullUntil.Sub(now))
	}
	return answer
}

func proxyPool(accounts []proxyacct.Account, now time.Time) []claudepick.Member {
	pool := []claudepick.Member{}
	for _, account := range accounts {
		if account.Provider != "claude" || account.Status == proxyacct.StatusDisabled || account.Status == proxyacct.StatusUnavailable {
			continue
		}
		member := claudepick.Member{Account: account.Label, RetryAt: account.NextRetry}
		for _, window := range account.Windows {
			if !window.ResetsAt.IsZero() && !now.Before(window.ResetsAt) {
				continue
			}
			reading := &claudepick.Window{Percent: window.Percent, ResetsAt: window.ResetsAt, Ceiling: float64(window.Ceiling)}
			switch window.Kind {
			case proxyacct.WindowSession:
				member.Session = reading
			case proxyacct.WindowWeek:
				member.Weekly = reading
			}
		}
		pool = append(pool, member)
	}
	return pool
}
