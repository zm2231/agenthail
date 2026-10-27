package claudepick

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	DefaultSessionCeiling = 90
	DefaultWeeklyCeiling  = 90
)

type Policy struct {
	Auto           bool    `json:"auto"`
	Preferred      string  `json:"preferred,omitempty"`
	SessionCeiling float64 `json:"sessionCeiling"`
	WeeklyCeiling  float64 `json:"weeklyCeiling"`
}

func DefaultPolicy() Policy {
	return Policy{Auto: true, SessionCeiling: DefaultSessionCeiling, WeeklyCeiling: DefaultWeeklyCeiling}
}

type Window struct {
	Percent  float64
	ResetsAt time.Time
}

type Candidate struct {
	Name    string
	Root    string
	Account string
	Login   string
	Primary bool
	Session *Window
	Weekly  *Window
}

// Pick is the chosen root. Root is empty for the primary root.
type Pick struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Account string `json:"account,omitempty"`
	Reason  string `json:"reason"`
}

func Choose(candidates []Candidate, policy Policy, now time.Time) Pick {
	if len(candidates) == 0 {
		return Pick{Reason: "no Claude account is configured"}
	}
	primary := candidates[0]
	for _, candidate := range candidates {
		if candidate.Primary {
			primary = candidate
			break
		}
	}
	if len(candidates) == 1 {
		return pick(primary, "the only account")
	}
	if !policy.Auto {
		return pick(primary, "automatic choice is off")
	}
	eligible := []Candidate{}
	var preferred *Candidate
	for _, candidate := range candidates {
		if candidate.Name == policy.Preferred && policy.Preferred != "" && candidate.Login != surface.ClaudeLoginSignedOut {
			preferred = &candidate
			eligible = append(eligible, candidate)
		} else if candidate.Login == surface.ClaudeLoginClaudeAI {
			eligible = append(eligible, candidate)
		}
	}
	if preferred != nil && preferred.Login == surface.ClaudeLoginAPIKey {
		return pick(*preferred, "preferred; uses an API key")
	}
	if preferred != nil && known(*preferred) && under(*preferred, policy) {
		return pick(*preferred, "preferred, "+readings(*preferred))
	}
	var open, full []Candidate
	for _, candidate := range eligible {
		switch {
		case !known(candidate):
		case under(candidate, policy):
			open = append(open, candidate)
		default:
			full = append(full, candidate)
		}
	}
	if len(open) > 0 {
		sort.SliceStable(open, func(i, j int) bool { return load(open[i], policy) < load(open[j], policy) })
		return pick(open[0], "most headroom, "+readings(open[0]))
	}
	if len(full) > 0 {
		sort.SliceStable(full, func(i, j int) bool { return frees(full[i], policy).Before(frees(full[j], policy)) })
		return pick(full[0], fmt.Sprintf("every account is near its limit; this one frees up first, in %s", until(frees(full[0], policy).Sub(now))))
	}
	if preferred != nil {
		return pick(*preferred, "preferred; no usage reading")
	}
	if len(eligible) == 0 {
		return pick(primary, "no account is signed in to claude.ai")
	}
	for _, candidate := range eligible {
		if candidate.Primary {
			return pick(candidate, "no usage reading")
		}
	}
	return pick(eligible[0], "no usage reading")
}

func pick(candidate Candidate, reason string) Pick {
	root := candidate.Root
	if candidate.Primary {
		root = ""
	}
	return Pick{Root: root, Name: candidate.Name, Account: candidate.Account, Reason: reason}
}

func known(c Candidate) bool { return c.Session != nil || c.Weekly != nil }

func under(c Candidate, policy Policy) bool {
	return (c.Session == nil || c.Session.Percent < policy.SessionCeiling) && (c.Weekly == nil || c.Weekly.Percent < policy.WeeklyCeiling)
}

func load(c Candidate, policy Policy) float64 {
	value := 0.0
	if c.Session != nil {
		value = math.Max(value, c.Session.Percent/policy.SessionCeiling)
	}
	if c.Weekly != nil {
		value = math.Max(value, c.Weekly.Percent/policy.WeeklyCeiling)
	}
	return value
}

func frees(c Candidate, policy Policy) time.Time {
	var at time.Time
	for _, window := range []struct {
		reading *Window
		ceiling float64
	}{{c.Session, policy.SessionCeiling}, {c.Weekly, policy.WeeklyCeiling}} {
		if window.reading != nil && window.reading.Percent >= window.ceiling && window.reading.ResetsAt.After(at) {
			at = window.reading.ResetsAt
		}
	}
	if at.IsZero() {
		return time.Unix(math.MaxInt32, 0)
	}
	return at
}

func readings(c Candidate) string {
	text := ""
	if c.Session != nil {
		text = fmt.Sprintf("session %.0f%%", c.Session.Percent)
	}
	if c.Weekly != nil {
		if text != "" {
			text += ", "
		}
		text += fmt.Sprintf("week %.0f%%", c.Weekly.Percent)
	}
	return text
}

func until(d time.Duration) string {
	if d < time.Minute {
		return "under a minute"
	}
	d = d.Round(time.Minute)
	days, hours, minutes := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
