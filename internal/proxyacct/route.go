package proxyacct

import (
	"sort"
	"time"
)

// Route is where a CLIProxyAPI proxy sends new work now, for a Fable model
// when fable is set. Routed names, per provider, the account its quota-drain
// selector picks. Full means every Claude account is cooling or at a ceiling
// that binds the request (session and week; the per-model week too for
// Fable): no Claude account is routed, Lead is the fullest one and FreesAt is
// when the first frees up, zero when any full account does not say when.
// Otherwise Lead is the routed Claude account, or another provider's on a
// proxy without Claude.
type Route struct {
	Routed  map[string]string
	Lead    string
	Full    bool
	FreesAt time.Time
}

const week = 7 * 24 * time.Hour

func Routing(accounts []Account, now time.Time, fable bool) Route {
	route := Route{Routed: map[string]string{}}
	route.FreesAt, route.Full = Full(accounts, now, fable)
	var providers []string
	for _, account := range accounts {
		if _, seen := route.Routed[account.Provider]; seen || (route.Full && account.Provider == "claude") {
			continue
		}
		if pick, ok := Pick(accounts, account.Provider, now, fable); ok {
			route.Routed[account.Provider] = pick.ID
			providers = append(providers, account.Provider)
		}
	}
	if route.Full {
		if fullest, ok := fullest(accounts, now); ok {
			route.Lead = fullest.ID
			return route
		}
		route.Full, route.FreesAt = false, time.Time{}
	}
	if id, ok := route.Routed["claude"]; ok {
		route.Lead = id
	} else if len(providers) > 0 {
		route.Lead = route.Routed[providers[0]]
	}
	return route
}

// Pick mirrors CLIProxyAPI's quota-drain selector for one provider, applying
// the per-model week's ceiling only for Fable as the proxy does; keep the order
// in step with it: live accounts only, those under
// their ceilings first, then the higher preference, then one with no weekly
// reset known yet, then the soonest weekly reset. It reads Drain, never the
// displayed Windows. The proxy's ties fall to its own credential order, which
// it does not report; here they keep the accounts' order.
func Pick(accounts []Account, provider string, now time.Time, fable bool) (Account, bool) {
	type candidate struct {
		account   Account
		atCeiling bool
		observed  bool
		reset     time.Time
	}
	var candidates []candidate
	for _, account := range accounts {
		if account.Provider != provider || !live(account, now) {
			continue
		}
		c := candidate{account: account}
		readings := drainReadings(account, now)
		c.atCeiling = len(atCeiling(readings, fable)) > 0
		c.reset = readings[WindowWeek].ResetsAt
		if c.reset.IsZero() {
			c.reset = readings[WindowWeekModel].ResetsAt
		}
		c.observed = !c.reset.IsZero()
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 {
		return Account{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.atCeiling != right.atCeiling {
			return !left.atCeiling
		}
		if left.account.Preference != right.account.Preference {
			return left.account.Preference > right.account.Preference
		}
		if left.observed != right.observed {
			return !left.observed
		}
		return left.reset.Before(right.reset)
	})
	return candidates[0].account, true
}

// Full reports whether no Claude account could take the work without going
// over a ceiling: each is cooling or at a ceiling that binds it.
// FreesAt is when the first frees up, zero when any full account does not say
// when, since that one may free up first.
func Full(accounts []Account, now time.Time, fable bool) (time.Time, bool) {
	var first time.Time
	usable, unknown := 0, false
	for _, account := range accounts {
		if account.Provider != "claude" || account.Status == StatusDisabled || account.Status == StatusUnavailable {
			continue
		}
		usable++
		frees, known := time.Time{}, true
		if !live(account, now) {
			frees = account.NextRetry
			known = frees.After(now)
		}
		over := atCeiling(drainReadings(account, now), fable)
		if live(account, now) && len(over) == 0 {
			return time.Time{}, false
		}
		for _, reading := range over {
			if reading.ResetsAt.IsZero() {
				known = false
			} else if reading.ResetsAt.After(frees) {
				frees = reading.ResetsAt
			}
		}
		if !known {
			unknown = true
		} else if first.IsZero() || frees.Before(first) {
			first = frees
		}
	}
	if unknown {
		first = time.Time{}
	}
	return first, usable > 0
}

func live(account Account, now time.Time) bool {
	return account.Status == StatusActive && !account.NextRetry.After(now)
}

var drainPeriods = map[string]time.Duration{WindowSession: 5 * time.Hour, WindowWeek: week, WindowWeekModel: week}

// A window whose reset has passed has started over: empty, with its next
// reset a full period out, as the proxy counts it.
func drainReadings(account Account, now time.Time) map[string]Drain {
	readings := map[string]Drain{}
	for _, reading := range account.Drain {
		if !reading.ResetsAt.IsZero() && !reading.ResetsAt.After(now) {
			reading.Percent, reading.HasPercent, reading.ResetsAt = 0, true, now.Add(drainPeriods[reading.Kind])
		}
		readings[reading.Kind] = reading
	}
	return readings
}

func atCeiling(readings map[string]Drain, fable bool) []Drain {
	kinds := []string{WindowSession, WindowWeek}
	if fable {
		kinds = append(kinds, WindowWeekModel)
	}
	var over []Drain
	for _, kind := range kinds {
		reading, ok := readings[kind]
		if ok && reading.Ceiling > 0 && reading.HasPercent && reading.Percent >= reading.Ceiling {
			over = append(over, reading)
		}
	}
	return over
}

func fullest(accounts []Account, now time.Time) (Account, bool) {
	var best Account
	top, found := -1.0, false
	for _, account := range accounts {
		if account.Provider != "claude" || account.Status == StatusDisabled || account.Status == StatusUnavailable {
			continue
		}
		percent := 0.0
		for _, window := range account.Windows {
			if (window.ResetsAt.IsZero() || window.ResetsAt.After(now)) && window.Percent > percent {
				percent = window.Percent
			}
		}
		if percent > top {
			best, top, found = account, percent, true
		}
	}
	return best, found
}
