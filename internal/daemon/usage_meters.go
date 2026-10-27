package daemon

import (
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Codex rollouts only update while Codex runs, so an older reading shows its age.
const usageAgedAfter = time.Hour

type usageMeter struct {
	Provider string    `json:"provider"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Scope    string    `json:"scope,omitempty"`
	Percent  float64   `json:"percent"`
	Severity string    `json:"severity,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
	AsOf     time.Time `json:"asOf"`
	Aged     bool      `json:"aged"`
	Until    time.Time `json:"until,omitzero"`
	Ceiling  float64   `json:"ceiling,omitempty"`
}

func usageMeters(provider string, usage *surface.Usage, now time.Time) []usageMeter {
	if usage == nil {
		return nil
	}
	var meters []usageMeter
	for _, limit := range usage.Limits {
		if limit.Kind == "weekly_scoped" && limit.Percent <= 0 {
			continue
		}
		if !limit.ResetsAt.IsZero() && !limit.ResetsAt.After(now) {
			continue
		}
		window, known := usageWindow(limit.Kind)
		if known && now.Sub(usage.FetchedAt) > window {
			continue
		}
		meter := usageMeter{
			Provider: provider,
			Kind:     limit.Kind,
			Label:    usageLabel(provider, limit.Kind, limit.Scope),
			Scope:    limit.Scope,
			Percent:  limit.Percent,
			Severity: limit.Severity,
			ResetsAt: limit.ResetsAt,
			AsOf:     usage.FetchedAt,
			Aged:     now.Sub(usage.FetchedAt) > usageAgedAfter,
			Until:    limit.ResetsAt,
		}
		if meter.Severity == "" && limit.Percent >= 90 {
			meter.Severity = "critical"
		}
		if known && (meter.Until.IsZero() || usage.FetchedAt.Add(window).Before(meter.Until)) {
			meter.Until = usage.FetchedAt.Add(window)
		}
		meters = append(meters, meter)
	}
	return meters
}

func usageLabel(provider, kind, scope string) string {
	name := map[string]string{"claude": "Claude", "codex": "Codex"}[provider]
	switch {
	case provider == "claude" && kind == "session":
		return name
	case kind == "weekly" || kind == "weekly_all":
		return name + " week"
	case kind == "weekly_scoped" && scope != "":
		return name + " week, " + scope
	case kind == "weekly_scoped":
		return name + " week, one model"
	}
	return name + " " + strings.ReplaceAll(kind, "_", " ")
}

func usageWindow(kind string) (time.Duration, bool) {
	switch kind {
	case "session", "5h":
		return 5 * time.Hour, true
	case "weekly", "weekly_all", "weekly_scoped":
		return 7 * 24 * time.Hour, true
	}
	if minutes, err := strconv.Atoi(strings.TrimSuffix(kind, "m")); err == nil && strings.HasSuffix(kind, "m") && minutes > 0 {
		return time.Duration(minutes) * time.Minute, true
	}
	return 0, false
}
