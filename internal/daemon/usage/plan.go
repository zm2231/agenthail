package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

const (
	PreferOn   = "on"
	PreferOff  = "off"
	PreferOnly = "only"
)

type PreferRequest struct {
	Proxy   string `json:"proxy"`
	Account string `json:"account"`
	Set     string `json:"set"`
	Now     bool   `json:"now,omitempty"`
	PlanID  string `json:"planId,omitempty"`
}

type PreferChange struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Provider string  `json:"provider"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
}

type PreferAccount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type PreferPlan struct {
	Proxy    *Proxy          `json:"proxy"`
	Host     string          `json:"host"`
	Account  PreferAccount   `json:"account"`
	Set      string          `json:"set"`
	Now      bool            `json:"now"`
	Changes  []PreferChange  `json:"changes"`
	Release  []PreferAccount `json:"release"`
	Hosts    []PreferHost    `json:"hosts"`
	Nothing  string          `json:"nothing,omitempty"`
	Confirm  string          `json:"confirm,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
	PlanID   string          `json:"planId"`
}

type PreferHost struct {
	Host     string          `json:"host"`
	HostID   string          `json:"hostId"`
	Roots    []string        `json:"roots"`
	Sessions []PreferSession `json:"sessions"`
}

type PreferSession struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PlanError struct {
	Status int
	Code   string
	Err    string
}

func (e *PlanError) Error() string { return e.Err }

func PlanPrefer(accounts []proxyacct.Account, request PreferRequest) (PreferPlan, error) {
	if !slices.Contains([]string{PreferOn, PreferOff, PreferOnly}, request.Set) {
		return PreferPlan{}, &PlanError{400, "invalid_set", "set must be on, off or only"}
	}
	if request.Now && request.Set != PreferOnly {
		return PreferPlan{}, &PlanError{400, "invalid_now", "now applies only to prefer only"}
	}
	index := slices.IndexFunc(accounts, func(account proxyacct.Account) bool { return account.ID == request.Account })
	if index < 0 {
		return PreferPlan{}, &PlanError{404, "no_account", fmt.Sprintf("The proxy has no account %q.", request.Account)}
	}
	target := accounts[index]
	plan := PreferPlan{Account: PreferAccount{ID: target.ID, Label: target.Label}, Set: request.Set, Now: request.Now, Changes: []PreferChange{}, Release: []PreferAccount{}, Hosts: []PreferHost{}}
	after := proxyacct.Preferred
	if request.Set == PreferOff {
		after = proxyacct.NotPreferred
	}
	if target.Preference != after {
		plan.Changes = append(plan.Changes, PreferChange{ID: target.ID, Label: target.Label, Provider: target.Provider, Before: target.Preference, After: after})
	}
	if request.Set == PreferOnly {
		for _, other := range accounts {
			if other.ID == target.ID || other.Provider != target.Provider || other.Status == proxyacct.StatusDisabled {
				continue
			}
			if other.Preference == proxyacct.NotPreferred {
				continue
			}
			plan.Changes = append(plan.Changes, PreferChange{ID: other.ID, Label: other.Label, Provider: other.Provider, Before: other.Preference, After: proxyacct.NotPreferred})
			if request.Now {
				plan.Release = append(plan.Release, PreferAccount{ID: other.ID, Label: other.Label})
			}
		}
	}
	if len(plan.Release) > 0 {
		plan.Warnings = append(plan.Warnings, "The proxy does not say which account each session is on. Sessions on the released accounts move on their next request and rebuild their prompt cache on the new account.")
	}
	if len(plan.Changes) == 0 && len(plan.Release) == 0 {
		plan.Nothing = map[string]string{PreferOn: "%s is already preferred.", PreferOff: "%s is already not preferred.", PreferOnly: "%s is already the only preferred account."}[request.Set]
		plan.Nothing = fmt.Sprintf(plan.Nothing, target.Label)
	}
	plan.PlanID = proxyPlanID(accounts, request)
	return plan, nil
}

// Covers every account, so any change on the proxy since the preview refuses the write.
func proxyPlanID(accounts []proxyacct.Account, request PreferRequest) string {
	parts := []string{request.Proxy, request.Account, request.Set, fmt.Sprint(request.Now)}
	for _, account := range accounts {
		parts = append(parts, fmt.Sprintf("%s=%g", account.ID, account.Preference))
	}
	slices.Sort(parts[4:])
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:16])
}

func (plan *PreferPlan) SetConfirm() {
	if plan.Nothing != "" {
		return
	}
	label := plan.Account.Label
	switch plan.Set {
	case PreferOn:
		plan.Confirm = "Prefer " + label
	case PreferOff:
		plan.Confirm = "Stop preferring " + label
	default:
		plan.Confirm = "Prefer only " + label
		off := []string{}
		for _, change := range plan.Changes {
			if change.ID != plan.Account.ID {
				off = append(off, change.Label)
			}
		}
		if len(off) > 0 {
			plan.Confirm += " (turns off " + strings.Join(off, ", ") + ")"
		}
		if moving := plan.runningSessions(); plan.Now && moving > 0 {
			plan.Confirm += fmt.Sprintf(" and move up to %d running %s", moving, plural(moving, "session", "sessions"))
		}
	}
}

func (plan PreferPlan) runningSessions() int {
	count := 0
	for _, host := range plan.Hosts {
		count += len(host.Sessions)
	}
	return count
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
