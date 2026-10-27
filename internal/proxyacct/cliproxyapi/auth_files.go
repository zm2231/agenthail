package cliproxyapi

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

type authFiles struct {
	Files []authFile `json:"files"`
}

type authFile struct {
	Name        string                `json:"name"`
	Provider    string                `json:"provider"`
	Type        string                `json:"type"`
	Email       string                `json:"email"`
	Label       string                `json:"label"`
	Status      string                `json:"status"`
	Disabled    bool                  `json:"disabled"`
	Unavailable bool                  `json:"unavailable"`
	NextRetry   time.Time             `json:"next_retry_after"`
	Cooldowns   []cooldown            `json:"cooldowns"`
	Preference  json.Number           `json:"quota_preference"`
	Ceiling     map[string]float64    `json:"quota_ceiling"`
	Quota       quota                 `json:"quota"`
	ModelQuotas map[string]modelState `json:"model_quotas"`
}

type cooldown struct {
	Scope   string    `json:"scope"`
	RetryAt time.Time `json:"retry_at"`
}

type quota struct {
	ObservedAt time.Time         `json:"observed_at"`
	Signals    map[string]string `json:"signals"`
}

type modelState struct {
	ObservedAt time.Time         `json:"observed_at"`
	Signals    map[string]string `json:"signals"`
	Quota      *quota            `json:"quota"`
}

type signal struct {
	value    string
	observed time.Time
}

func (a authFiles) accounts(now time.Time) []proxyacct.Account {
	accounts := []proxyacct.Account{}
	for _, file := range a.Files {
		provider := file.Provider
		if provider == "" {
			provider = file.Type
		}
		if provider != "claude" && provider != "codex" {
			continue
		}
		label := file.Email
		if label == "" {
			label = file.Label
		}
		if label == "" {
			label = strings.TrimSuffix(file.Name, ".json")
		}
		preference, _ := file.Preference.Float64()
		status, retry := file.status(now)
		signals := file.signals()
		windows := claudeWindows(signals, file.Ceiling)
		if provider == "codex" {
			windows = codexWindows(signals)
		}
		accounts = append(accounts, proxyacct.Account{ID: file.Name, Provider: provider, Label: label, Status: status, NextRetry: retry, Preference: preference, Windows: windows})
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Provider != accounts[j].Provider {
			return accounts[i].Provider < accounts[j].Provider
		}
		return accounts[i].Label < accounts[j].Label
	})
	return accounts
}

func (f authFile) status(now time.Time) (string, time.Time) {
	if f.Disabled {
		return proxyacct.StatusDisabled, time.Time{}
	}
	var retry time.Time
	for _, cooldown := range f.Cooldowns {
		if cooldown.Scope == "credential" && cooldown.RetryAt.After(now) && cooldown.RetryAt.After(retry) {
			retry = cooldown.RetryAt
		}
	}
	if f.NextRetry.After(now) && f.NextRetry.After(retry) {
		retry = f.NextRetry
	}
	switch {
	case !retry.IsZero():
		return proxyacct.StatusCooling, retry
	case f.Unavailable || (f.Status != "" && f.Status != "active"):
		return proxyacct.StatusUnavailable, time.Time{}
	}
	return proxyacct.StatusActive, time.Time{}
}

// The credential keeps the last response's headers; a window only some models
// report, such as the per-model week, lives on that model's newest state.
func (f authFile) signals() map[string]signal {
	merged := map[string]signal{}
	for key, value := range f.Quota.Signals {
		merged[strings.ToLower(key)] = signal{value: value, observed: f.Quota.ObservedAt}
	}
	for _, state := range f.ModelQuotas {
		source := quota{ObservedAt: state.ObservedAt, Signals: state.Signals}
		if state.Quota != nil {
			source = *state.Quota
		}
		for key, value := range source.Signals {
			key = strings.ToLower(key)
			if current, ok := merged[key]; !ok || source.ObservedAt.After(current.observed) {
				merged[key] = signal{value: value, observed: source.ObservedAt}
			}
		}
	}
	return merged
}

var claudeWindowKinds = []struct{ header, kind string }{
	{"5h", proxyacct.WindowSession},
	{"7d", proxyacct.WindowWeek},
	{"7d_oi", proxyacct.WindowWeekModel},
}

func claudeWindows(signals map[string]signal, ceilings map[string]float64) []proxyacct.Window {
	windows := []proxyacct.Window{}
	for _, window := range claudeWindowKinds {
		prefix := "anthropic-ratelimit-unified-" + window.header + "-"
		utilization, ok := signals[prefix+"utilization"]
		fraction, err := strconv.ParseFloat(strings.TrimSpace(utilization.value), 64)
		if !ok || err != nil {
			continue
		}
		windows = append(windows, proxyacct.Window{Kind: window.kind, Percent: fraction * 100, ResetsAt: unixTime(signals[prefix+"reset"].value), ObservedAt: utilization.observed, Ceiling: ceilings[window.header]})
	}
	return windows
}

func codexWindows(signals map[string]signal) []proxyacct.Window {
	windows := []proxyacct.Window{}
	for _, name := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + name + "-"
		used, ok := signals[prefix+"used-percent"]
		percent, err := strconv.ParseFloat(strings.TrimSpace(used.value), 64)
		if !ok || err != nil {
			continue
		}
		kind := proxyacct.WindowSession
		if minutes, err := strconv.Atoi(strings.TrimSpace(signals[prefix+"window-minutes"].value)); err == nil && minutes > 24*60 {
			kind = proxyacct.WindowWeek
		} else if err != nil && name == "secondary" {
			kind = proxyacct.WindowWeek
		}
		windows = append(windows, proxyacct.Window{Kind: kind, Percent: percent, ResetsAt: unixTime(signals[prefix+"reset-at"].value), ObservedAt: used.observed})
	}
	return windows
}

func unixTime(value string) time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
