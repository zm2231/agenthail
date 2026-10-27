package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

const (
	preferOn   = "on"
	preferOff  = "off"
	preferOnly = "only"
)

type proxyPreferRequest struct {
	Proxy   string `json:"proxy"`
	Account string `json:"account"`
	Set     string `json:"set"`
	Now     bool   `json:"now,omitempty"`
	PlanID  string `json:"planId,omitempty"`
}

type proxyPreferChange struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Provider string  `json:"provider"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
}

type proxyPreferAccount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type proxyPreferPlan struct {
	Proxy    *usageProxy          `json:"proxy"`
	Host     string               `json:"host"`
	Account  proxyPreferAccount   `json:"account"`
	Set      string               `json:"set"`
	Now      bool                 `json:"now"`
	Changes  []proxyPreferChange  `json:"changes"`
	Release  []proxyPreferAccount `json:"release"`
	Hosts    []proxyPreferHost    `json:"hosts"`
	Nothing  string               `json:"nothing,omitempty"`
	Confirm  string               `json:"confirm,omitempty"`
	Warnings []string             `json:"warnings,omitempty"`
	PlanID   string               `json:"planId"`
}

type proxyPreferHost struct {
	Host     string               `json:"host"`
	HostID   string               `json:"hostId"`
	Roots    []string             `json:"roots"`
	Sessions []proxyPreferSession `json:"sessions"`
}

type proxyPreferSession struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type proxyPlanError struct {
	status int
	code   string
	err    string
}

func (e *proxyPlanError) Error() string { return e.err }

func planProxyPrefer(accounts []proxyacct.Account, request proxyPreferRequest) (proxyPreferPlan, error) {
	if !slices.Contains([]string{preferOn, preferOff, preferOnly}, request.Set) {
		return proxyPreferPlan{}, &proxyPlanError{400, "invalid_set", "set must be on, off or only"}
	}
	if request.Now && request.Set != preferOnly {
		return proxyPreferPlan{}, &proxyPlanError{400, "invalid_now", "now applies only to prefer only"}
	}
	index := slices.IndexFunc(accounts, func(account proxyacct.Account) bool { return account.ID == request.Account })
	if index < 0 {
		return proxyPreferPlan{}, &proxyPlanError{404, "no_account", fmt.Sprintf("The proxy has no account %q.", request.Account)}
	}
	target := accounts[index]
	plan := proxyPreferPlan{Account: proxyPreferAccount{ID: target.ID, Label: target.Label}, Set: request.Set, Now: request.Now, Changes: []proxyPreferChange{}, Release: []proxyPreferAccount{}, Hosts: []proxyPreferHost{}}
	after := proxyacct.Preferred
	if request.Set == preferOff {
		after = proxyacct.NotPreferred
	}
	if target.Preference != after {
		plan.Changes = append(plan.Changes, proxyPreferChange{ID: target.ID, Label: target.Label, Provider: target.Provider, Before: target.Preference, After: after})
	}
	if request.Set == preferOnly {
		for _, other := range accounts {
			if other.ID == target.ID || other.Provider != target.Provider || other.Status == proxyacct.StatusDisabled {
				continue
			}
			if other.Preference != proxyacct.NotPreferred {
				plan.Changes = append(plan.Changes, proxyPreferChange{ID: other.ID, Label: other.Label, Provider: other.Provider, Before: other.Preference, After: proxyacct.NotPreferred})
			}
			if request.Now {
				plan.Release = append(plan.Release, proxyPreferAccount{ID: other.ID, Label: other.Label})
			}
		}
	}
	if len(plan.Release) > 0 {
		plan.Warnings = append(plan.Warnings, "The proxy does not say which account each session is on. Sessions on the released accounts move on their next request and rebuild their prompt cache on the new account.")
	}
	if len(plan.Changes) == 0 && len(plan.Release) == 0 {
		plan.Nothing = map[string]string{preferOn: "%s is already preferred.", preferOff: "%s is already not preferred.", preferOnly: "%s is already the only preferred account."}[request.Set]
		plan.Nothing = fmt.Sprintf(plan.Nothing, target.Label)
	}
	plan.PlanID = proxyPlanID(accounts, request)
	return plan, nil
}

// Covers every account, so any change on the proxy since the preview refuses the write.
func proxyPlanID(accounts []proxyacct.Account, request proxyPreferRequest) string {
	parts := []string{request.Proxy, request.Account, request.Set, fmt.Sprint(request.Now)}
	for _, account := range accounts {
		parts = append(parts, fmt.Sprintf("%s=%g", account.ID, account.Preference))
	}
	slices.Sort(parts[4:])
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:16])
}

func (plan *proxyPreferPlan) confirm() {
	if plan.Nothing != "" {
		return
	}
	label := plan.Account.Label
	switch plan.Set {
	case preferOn:
		plan.Confirm = "Prefer " + label
	case preferOff:
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

func (plan proxyPreferPlan) runningSessions() int {
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
