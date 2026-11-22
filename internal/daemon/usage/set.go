package usage

import (
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/surface"
)

// A set with no provider is a host that could not answer.
type Set struct {
	Host        string    `json:"host"`
	HostID      string    `json:"hostId"`
	Local       bool      `json:"local"`
	Provider    string    `json:"provider,omitempty"`
	Name        string    `json:"name,omitempty"`
	Account     string    `json:"account,omitempty"`
	Login       string    `json:"login,omitempty"`
	LoginSource string    `json:"loginSource,omitempty"`
	Root        string    `json:"root,omitempty"`
	Primary     bool      `json:"primary,omitempty"`
	Meters      []Meter   `json:"meters"`
	AsOf        time.Time `json:"asOf,omitzero"`
	Problem     string    `json:"problem,omitempty"`
	Detail      string    `json:"problemDetail,omitempty"`

	Proxy        *Proxy    `json:"proxy,omitempty"`
	Accounts     []Account `json:"accounts,omitempty"`
	ProxyProblem string    `json:"proxyProblem,omitempty"`
	Routing      *Routing  `json:"routing,omitempty"`
}

func ClaudeNoUsageProblem(login, source string) (problem, detail string, ok bool) {
	switch login {
	case surface.ClaudeLoginAPIKey:
		uses := surface.ClaudeLoginPhrase(source)
		return "Signed in with " + uses + "; no usage reported.", "claude.ai reports usage only for a claude.ai login, and this config root uses " + uses + ".", true
	case surface.ClaudeLoginSignedOut:
		return "Not signed in; no usage reported.", "This config root holds no login. Run /login in a Claude session that uses it to sign in.", true
	}
	return "", "", false
}

type Proxy struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	URL  string `json:"url,omitempty"`
}

type Routing struct {
	Account string    `json:"account,omitempty"`
	Full    bool      `json:"full,omitempty"`
	FreesAt time.Time `json:"freesAt,omitzero"`
}

type Account struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	Label      string    `json:"label"`
	Status     string    `json:"status"`
	NextRetry  time.Time `json:"nextRetry,omitzero"`
	Preference float64   `json:"preference"`
	Preferred  bool      `json:"preferred,omitempty"`
	Routed     bool      `json:"routed,omitempty"`
	SetOn      string    `json:"setOn,omitempty"`
	SetAt      time.Time `json:"setAt,omitzero"`
	Meters     []Meter   `json:"meters"`
	AsOf       time.Time `json:"asOf,omitzero"`
}

func ProxyAccount(account proxyacct.Account, now time.Time) Account {
	view := Account{ID: account.ID, Provider: account.Provider, Label: account.Label, Status: account.Status, NextRetry: account.NextRetry, Preference: account.Preference, Meters: []Meter{}}
	for _, window := range account.Windows {
		kind := map[string]string{proxyacct.WindowSession: "session", proxyacct.WindowWeek: "weekly", proxyacct.WindowWeekModel: "weekly_scoped"}[window.Kind]
		usage := &surface.Usage{FetchedAt: window.ObservedAt, Limits: []surface.UsageWindow{{Kind: kind, Percent: window.Percent, ResetsAt: window.ResetsAt}}}
		for _, meter := range Meters(account.Provider, usage, now) {
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

func ProxyPreferred(accounts []proxyacct.Account, account proxyacct.Account) bool {
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

func ProxyProblemText(config proxyacct.Config, err error) (string, string) {
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

func ProxyWait(d time.Duration) string {
	d = d.Round(time.Minute)
	if days := int(d.Hours()) / 24; days > 0 {
		return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
	}
	if hours := int(d.Hours()); hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
}

const ReadingProblem = "reading"

// Hosts that share a proxy each report it; it shows once, read from a host
// that holds a working key when any does.
func MergeProxySets(sets []Set) []Set {
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
		if !readable[id] && sets[best[id]].ProxyProblem == string(proxyacct.KeyMissing) && set.ProxyProblem != string(proxyacct.KeyMissing) {
			best[id] = i
		}
		if set.ProxyProblem != string(proxyacct.KeyMissing) {
			keyless[id] = false
		}
	}
	merged := make([]Set, 0, len(sets))
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
