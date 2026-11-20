package daemon

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

type usageProxy struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	URL  string `json:"url,omitempty"`
}

type usageRouting struct {
	Account string    `json:"account,omitempty"`
	Full    bool      `json:"full,omitempty"`
	FreesAt time.Time `json:"freesAt,omitzero"`
}

type usageAccount struct {
	ID         string       `json:"id"`
	Provider   string       `json:"provider"`
	Label      string       `json:"label"`
	Status     string       `json:"status"`
	NextRetry  time.Time    `json:"nextRetry,omitzero"`
	Preference float64      `json:"preference"`
	Preferred  bool         `json:"preferred,omitempty"`
	Routed     bool         `json:"routed,omitempty"`
	SetOn      string       `json:"setOn,omitempty"`
	SetAt      time.Time    `json:"setAt,omitzero"`
	Meters     []usageMeter `json:"meters"`
	AsOf       time.Time    `json:"asOf,omitzero"`
}

func (d *Daemon) proxyUsageSets(now time.Time) []usageSet {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	configs, err := surface.ConfiguredProxies(home)
	if err != nil {
		return []usageSet{{Provider: "proxy", Problem: err.Error()}}
	}
	d.proxyUsage.retain(configs)
	sets := make([]usageSet, len(configs))
	var wait sync.WaitGroup
	for i, config := range configs {
		wait.Go(func() { sets[i] = d.proxyUsageSet(config, now) })
	}
	wait.Wait()
	return sets
}

func (d *Daemon) proxyIdentity(config proxyacct.Config) *usageProxy {
	id, _ := proxyacct.Endpoint(config.URL)
	if proxyacct.IsLoopback(id) {
		id += "@" + d.localHost().ID
	}
	return &usageProxy{Name: config.Name, ID: id, URL: config.URL}
}

func (d *Daemon) proxyUsageSet(config proxyacct.Config, now time.Time) usageSet {
	set := usageSet{Provider: "proxy", Name: config.Name, Proxy: d.proxyIdentity(config), Accounts: []usageAccount{}}
	reading := d.proxyUsage.cached(config, now)
	if reading == nil {
		set.ProxyProblem = proxyReadingProblem
		set.Problem = "Reading the " + config.Name + " proxy's accounts."
		return set
	}
	if reading.err != nil {
		set.ProxyProblem = string(proxyacct.ProblemOf(reading.err))
		set.Problem, set.Detail = proxyProblemText(config, reading.err)
		return set
	}
	set.AsOf = reading.readAt
	entry := d.proxyUsage.entry(config)
	route := proxyacct.Routing(reading.accounts, now, false)
	if route.Lead != "" {
		set.Routing = &usageRouting{Account: route.Lead, Full: route.Full, FreesAt: route.FreesAt}
	}
	for _, account := range reading.accounts {
		view := proxyUsageAccount(account, now)
		view.Preferred = proxyPreferred(reading.accounts, account)
		view.Routed = route.Routed[account.Provider] == account.ID
		if mark, ok := entry.markFor(account.ID, account.Preference); ok {
			view.SetOn, view.SetAt = mark.host, mark.at
		}
		set.Accounts = append(set.Accounts, view)
	}
	if route.Full {
		set.Problem = fmt.Sprintf("Every %s Claude account is over its ceiling.", config.Name)
		if route.FreesAt.After(now) {
			set.Problem = fmt.Sprintf("Every %s Claude account is over its ceiling; the first frees up in %s.", config.Name, proxyWait(route.FreesAt.Sub(now)))
		}
	}
	return set
}

func proxyUsageAccount(account proxyacct.Account, now time.Time) usageAccount {
	view := usageAccount{ID: account.ID, Provider: account.Provider, Label: account.Label, Status: account.Status, NextRetry: account.NextRetry, Preference: account.Preference, Meters: []usageMeter{}}
	for _, window := range account.Windows {
		kind := map[string]string{proxyacct.WindowSession: "session", proxyacct.WindowWeek: "weekly", proxyacct.WindowWeekModel: "weekly_scoped"}[window.Kind]
		usage := &surface.Usage{FetchedAt: window.ObservedAt, Limits: []surface.UsageWindow{{Kind: kind, Percent: window.Percent, ResetsAt: window.ResetsAt}}}
		for _, meter := range usageMeters(account.Provider, usage, now) {
			meter.Ceiling = window.Ceiling
			if window.Ceiling > 0 && meter.Percent >= window.Ceiling {
				meter.Severity = "critical"
			}
			view.Meters = append(view.Meters, meter)
		}
		if window.ObservedAt.After(view.AsOf) {
			view.AsOf = window.ObservedAt
		}
	}
	return view
}

func proxyPreferred(accounts []proxyacct.Account, account proxyacct.Account) bool {
	if account.Status == proxyacct.StatusDisabled {
		return false
	}
	below := false
	for _, other := range accounts {
		if other.Provider != account.Provider || other.Status == proxyacct.StatusDisabled {
			continue
		}
		if other.Preference > account.Preference {
			return false
		}
		below = below || other.Preference < account.Preference
	}
	return below
}

func proxyProblemText(config proxyacct.Config, err error) (string, string) {
	keyFix := fmt.Sprintf("Update the key reference with `agenthail proxies key %s --key-file <path>` (or --keychain-service <service> --keychain-account <account>, or --key-env <name>).", config.Name)
	switch proxyacct.ProblemOf(err) {
	case proxyacct.Unreachable:
		return fmt.Sprintf("%s proxy unreachable at %s: %v", config.Name, config.URL, err), ""
	case proxyacct.KeyMissing:
		return fmt.Sprintf("No management key for the %s proxy on this host; its accounts are not readable here.", config.Name), fmt.Sprintf("Add one with `agenthail proxies key %s --key-file <path>`. A paired host that has a key shows the accounts instead.", config.Name)
	case proxyacct.KeyUnreadable:
		return fmt.Sprintf("%s proxy: %v", config.Name, err), keyFix
	case proxyacct.KeyRefused:
		return fmt.Sprintf("%s proxy refused the management key from %s; Agenthail stops asking until the key changes.", config.Name, config.Management.Describe()), keyFix
	case proxyacct.Banned:
		return fmt.Sprintf("%s proxy: %v", config.Name, err), keyFix
	case proxyacct.RemoteOff:
		return fmt.Sprintf("%s proxy does not allow management from this host (remote management off).", config.Name), "Turn on remote-management.allow-remote in the proxy's config, or read it from the host it runs on."
	case proxyacct.NoManagement:
		return fmt.Sprintf("%s proxy has no management key set.", config.Name), "Set remote-management.secret-key or MANAGEMENT_PASSWORD on the proxy to let Agenthail read its accounts."
	}
	return fmt.Sprintf("%s proxy: %v", config.Name, err), ""
}

func proxyWait(d time.Duration) string {
	d = d.Round(time.Minute)
	if days := int(d.Hours()) / 24; days > 0 {
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
	if hours := int(d.Hours()); hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
}
