package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestUsageMetersKeepOnlyLimitsAClientCanStillShow(t *testing.T) {
	fetched := time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)
	resets := fetched.Add(74 * time.Hour)
	codex := &surface.Usage{FetchedAt: fetched, Limits: []surface.UsageWindow{
		{Kind: "5h", Percent: 21, ResetsAt: fetched.Add(90 * time.Minute)},
		{Kind: "weekly", Percent: 19, ResetsAt: resets},
	}}

	live := usageMeters("codex", codex, fetched.Add(10*time.Minute))
	if len(live) != 2 || live[0].Label != "Codex 5h" || live[1].Label != "Codex week" || live[1].Aged || !live[1].Until.Equal(resets) || !live[1].AsOf.Equal(fetched) {
		t.Fatalf("live=%+v", live)
	}

	aged := usageMeters("codex", codex, fetched.Add(2*time.Hour))
	if len(aged) != 1 || aged[0].Kind != "weekly" || !aged[0].Aged {
		t.Fatalf("a reading past its 5h reset drops that limit and an hour-old reading shows its age: %+v", aged)
	}
	if got := usageMeters("codex", codex, resets); len(got) != 0 {
		t.Fatalf("a limit at its reset is gone: %+v", got)
	}

	unreset := &surface.Usage{FetchedAt: fetched, Limits: []surface.UsageWindow{{Kind: "weekly", Percent: 40}}}
	if got := usageMeters("codex", unreset, fetched.Add(time.Hour)); len(got) != 1 || !got[0].Until.Equal(fetched.Add(7*24*time.Hour)) {
		t.Fatalf("a limit with no reset lasts its window from the reading: %+v", got)
	}
	if got := usageMeters("codex", unreset, fetched.Add(8*24*time.Hour)); len(got) != 0 {
		t.Fatalf("a reading older than its window is gone: %+v", got)
	}

	claude := &surface.Usage{FetchedAt: fetched, Limits: []surface.UsageWindow{
		{Kind: "session", Percent: 93, ResetsAt: fetched.Add(time.Hour)},
		{Kind: "weekly_all", Percent: 62, Severity: "warning", ResetsAt: resets},
		{Kind: "weekly_scoped", Percent: 0, ResetsAt: resets},
		{Kind: "90m", Percent: 5},
	}}
	got := usageMeters("claude", claude, fetched)
	labels := []string{}
	for _, meter := range got {
		labels = append(labels, meter.Label)
	}
	if strings.Join(labels, "|") != "Claude|Claude week|Claude 90m" {
		t.Fatalf("labels=%v", labels)
	}
	if got[0].Severity != "critical" || got[1].Severity != "warning" || !got[2].Until.Equal(fetched.Add(90*time.Minute)) {
		t.Fatalf("meters=%+v", got)
	}
	if usageMeters("codex", nil, fetched) != nil {
		t.Fatal("no reading gives no meters")
	}
}
