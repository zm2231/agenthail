package daemon

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) proxyUsageSets(now time.Time) []usage.Set {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	configs, err := surface.ConfiguredProxies(home)
	if err != nil {
		return []usage.Set{{Provider: "proxy", Problem: err.Error()}}
	}
	d.proxyUsage.Retain(configs)
	sets := make([]usage.Set, len(configs))
	var wait sync.WaitGroup
	for i, config := range configs {
		wait.Go(func() { sets[i] = d.proxyUsageSet(config, now) })
	}
	wait.Wait()
	return sets
}

func (d *Daemon) proxyIdentity(config proxyacct.Config) *usage.Proxy {
	id, _ := proxyacct.Endpoint(config.URL)
	if proxyacct.IsLoopback(id) {
		id += "@" + d.localHost().ID
	}
	return &usage.Proxy{Name: config.Name, ID: id, URL: config.URL}
}

func (d *Daemon) proxyUsageSet(config proxyacct.Config, now time.Time) usage.Set {
	set := usage.Set{Provider: "proxy", Name: config.Name, Proxy: d.proxyIdentity(config), Accounts: []usage.Account{}}
	reading := d.proxyUsage.Cached(config, now)
	if reading == nil {
		set.ProxyProblem = usage.ReadingProblem
		set.Problem = "Reading the " + config.Name + " proxy's accounts."
		return set
	}
	if reading.Err != nil {
		set.ProxyProblem = string(proxyacct.ProblemOf(reading.Err))
		set.Problem, set.Detail = usage.ProxyProblemText(config, reading.Err)
		return set
	}
	set.AsOf = reading.ReadAt
	entry := d.proxyUsage.Entry(config)
	route := proxyacct.Routing(reading.Accounts, now, false)
	if route.Lead != "" {
		set.Routing = &usage.Routing{Account: route.Lead, Full: route.Full, FreesAt: route.FreesAt}
	}
	for _, account := range reading.Accounts {
		view := usage.ProxyAccount(account, now)
		view.Preferred = usage.ProxyPreferred(reading.Accounts, account)
		view.Routed = route.Routed[account.Provider] == account.ID
		if mark, ok := entry.MarkFor(account.ID, account.Preference); ok {
			view.SetOn, view.SetAt = mark.Host, mark.At
		}
		set.Accounts = append(set.Accounts, view)
	}
	if route.Full {
		set.Problem = fmt.Sprintf("Every %s Claude account is over its ceiling.", config.Name)
		if route.FreesAt.After(now) {
			set.Problem = fmt.Sprintf("Every %s Claude account is over its ceiling; the first frees up in %s.", config.Name, usage.ProxyWait(route.FreesAt.Sub(now)))
		}
	}
	return set
}
