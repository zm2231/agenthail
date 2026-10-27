package daemon

import (
	"os"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) rootProxy(name string) *usageProxy {
	home, err := os.UserHomeDir()
	if err != nil {
		return &usageProxy{Name: name}
	}
	configs, _ := surface.ConfiguredProxies(home)
	for _, config := range configs {
		if config.Name == name {
			return d.proxyIdentity(config)
		}
	}
	return &usageProxy{Name: name}
}

// Hosts that share a proxy each report it; it shows once, read from a host
// that holds a working key when any does.
func mergeProxySets(sets []usageSet) []usageSet {
	best := map[string]int{}
	readable := map[string]bool{}
	keyless := map[string]bool{}
	for i, set := range sets {
		if set.Provider != "proxy" || set.Proxy == nil {
			continue
		}
		id := set.Proxy.ID
		if _, seen := best[id]; !seen {
			best[id], keyless[id] = i, true
		}
		if set.ProxyProblem == "" && !readable[id] {
			best[id], readable[id] = i, true
		}
		if set.ProxyProblem != string(proxyacct.KeyMissing) {
			keyless[id] = false
		}
	}
	merged := make([]usageSet, 0, len(sets))
	placed := map[string]bool{}
	for _, set := range sets {
		if set.Provider != "proxy" || set.Proxy == nil {
			merged = append(merged, set)
			continue
		}
		id := set.Proxy.ID
		if placed[id] {
			continue
		}
		placed[id] = true
		chosen := sets[best[id]]
		if keyless[id] && !readable[id] {
			chosen.Problem = "Accounts not readable from any host: no host has a management key for the " + chosen.Name + " proxy."
		}
		merged = append(merged, chosen)
	}
	return merged
}
