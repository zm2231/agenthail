package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionDisplayPrecedence(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	background := &surface.BackgroundTasks{Running: true}
	for _, tc := range []struct {
		name        string
		row         dashboardSession
		kind, label string
	}{
		{"idle waiting", dashboardSession{Status: surface.StatusIdle, WaitingOn: &surface.Waiting{Answerable: true}}, "waiting", "Waiting on you"},
		{"busy waiting", dashboardSession{Status: surface.StatusBusy, WaitingOn: &surface.Waiting{}}, "waiting", "Waiting on you"},
		{"busy limit", dashboardSession{Status: surface.StatusBusy, UsageLimit: &surface.UsageLimit{}}, "working", "Working"},
		{"background limit", dashboardSession{BackgroundTasks: background, UsageLimit: &surface.UsageLimit{}}, "limited", "Usage limit"},
		{"background", dashboardSession{BackgroundTasks: background, QueueCount: 2}, "finishing", "Finishing"},
		{"queued offline", dashboardSession{Status: surface.StatusOffline, QueueCount: 1}, "queued", "Queued"},
		{"offline", dashboardSession{Status: surface.StatusOffline}, "offline", "Offline"},
		{"unknown", dashboardSession{Status: surface.StatusUnknown, WaitingOn: &surface.Waiting{}}, "unavailable", "Unavailable"},
		{"remote down", dashboardSession{Host: &dashboardHost{State: "unreachable"}, Status: surface.StatusBusy}, "unavailable", "Unavailable"},
		{"remote diagnostic", dashboardSession{Host: &dashboardHost{State: "connected"}, UnavailableReason: "path missing", Status: surface.StatusBusy}, "working", "Working"},
		{"local diagnostic", dashboardSession{Host: &dashboardHost{State: "local"}, UnavailableReason: "path missing", Open: true}, "idle", "Ready"},
		{"expired limit", dashboardSession{UsageLimit: &surface.UsageLimit{ResetsAt: now}}, "idle", "Closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionDisplay(tc.row, now)
			if got.Kind != tc.kind || got.Label != tc.label {
				t.Fatalf("display=%+v", got)
			}
		})
	}
}

func TestSessionDisplayInstantsAndStaleness(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.FixedZone("Source", 3600))
	for _, state := range []registry.LimitResumeState{registry.LimitResumeWaiting, registry.LimitResumeCanceled, registry.LimitResumeLapsed, registry.LimitResumeReleased} {
		row := dashboardSession{LimitResume: &dashboardLimitResume{State: state, ResetsAt: now.Add(time.Hour)}}
		got := sessionDisplay(row, now)
		if (got.ResumesAt != nil) != (state == registry.LimitResumeWaiting) {
			t.Fatalf("%s: %+v", state, got)
		}
		if got.ResumesAt != nil && got.ResumesAt.Location() != time.UTC {
			t.Fatal(got)
		}
	}
	last := now.Add(-10 * time.Minute)
	row := dashboardSession{Status: surface.StatusBusy, Activity: &surface.Activity{LastProgressAt: last, Tool: &surface.RunningTool{Name: "Bash", StartedAt: last}}, Freshness: &registry.CatalogFreshness{Stale: true}}
	got := sessionDisplay(row, now)
	if got.Kind != "working" || got.QuietSince == nil || got.ToolStartedAt == nil || !strings.Contains(got.Detail, "stale") {
		t.Fatal(got)
	}
	if before := sessionDisplay(row, now.Add(-time.Nanosecond)); before.QuietSince != nil {
		t.Fatal(before)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"quietSince":"2026-10-25T10:50:00Z"`) || strings.Contains(got.Detail, "10:50") || strings.Contains(got.Detail, "UTC") {
		t.Fatal(string(raw))
	}
	got = sessionDisplay(dashboardSession{UsageLimit: &surface.UsageLimit{ResetsAt: now.Add(time.Hour)}}, now)
	if got.ResetsAt == nil || got.ResumesAt != nil {
		t.Fatal(got)
	}
}
