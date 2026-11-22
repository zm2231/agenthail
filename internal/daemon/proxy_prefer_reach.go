package daemon

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) proxySetsEverywhere(ctx context.Context, now time.Time) []usage.Set {
	var proxies, remote []usage.Set
	var wait sync.WaitGroup
	wait.Go(func() { proxies = d.proxyUsageSets(now) })
	wait.Go(func() { remote = d.remoteUsageSets(ctx) })
	wait.Wait()
	local := d.localHost()
	sets := []usage.Set{}
	for _, root := range d.currentClaudeConfigRoots() {
		if root.Proxy != "" {
			sets = append(sets, usage.Set{Provider: "claude", Name: root.Name, Root: root.Root, Login: "proxy", Proxy: d.rootProxy(root.Proxy)})
		}
	}
	sets = append(sets, proxies...)
	for i := range sets {
		sets[i].Host, sets[i].HostID, sets[i].Local = local.Name, local.ID, true
	}
	return usage.MergeProxySets(append(sets, remote...))
}

func (d *Daemon) proxyReach(ctx context.Context, sets []usage.Set, id string) ([]usage.PreferHost, error) {
	hosts := []usage.PreferHost{}
	for _, set := range sets {
		if set.Provider != "claude" || set.Proxy == nil || set.Proxy.ID != id {
			continue
		}
		index := slices.IndexFunc(hosts, func(host usage.PreferHost) bool { return host.HostID == set.HostID })
		if index < 0 {
			hosts = append(hosts, usage.PreferHost{Host: set.Host, HostID: set.HostID, Roots: []string{}, Sessions: []usage.PreferSession{}})
			index = len(hosts) - 1
		}
		hosts[index].Roots = append(hosts[index].Roots, set.Root)
	}
	if len(hosts) == 0 {
		return hosts, nil
	}
	state, err := d.dashboardState(ctx)
	if err != nil {
		return hosts, err
	}
	for _, row := range state.Sessions {
		if row.Archived || row.Surface != surface.SurfaceKind("claude") || row.ClaudeConfigRoot == nil || row.Host == nil || !(SessionWorking(row.Status, row.Subagents) || row.Current) {
			continue
		}
		for i := range hosts {
			if hosts[i].HostID == row.Host.ID && slices.Contains(hosts[i].Roots, row.ClaudeConfigRoot.Root) {
				name := row.Alias
				if name == "" {
					name = row.Name
				}
				hosts[i].Sessions = append(hosts[i].Sessions, usage.PreferSession{ID: row.ID, Name: name})
			}
		}
	}
	return hosts, nil
}
