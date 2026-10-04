package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestGoalHumanOutputPreservesUsageAndAttention(t *testing.T) {
	budget := int64(1000)
	at := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	for _, status := range []string{"active", "paused", "blocked", "usageLimited", "budgetLimited", "complete"} {
		goal := &surface.GoalState{Objective: "Ship", Status: status, TimeUsedSeconds: 72, TokensUsed: 480, TokenBudget: &budget, CreatedAt: at, UpdatedAt: at.Add(time.Minute)}
		output := formatGoalState(goal)
		for _, value := range []string{"Ship [" + status + "]", "Elapsed: 72s", "Tokens: 480 / 1000", "Created: 2026-10-04T08:00:00Z", "Updated: 2026-10-04T08:01:00Z"} {
			if !strings.Contains(output, value) {
				t.Fatalf("status=%s missing %q in %s", status, value, output)
			}
		}
		wantAttention := status == "blocked" || status == "usageLimited" || status == "budgetLimited"
		if strings.Contains(output, "Needs you") != wantAttention {
			t.Fatalf("status=%s output=%s", status, output)
		}
	}
	output := formatGoalState(&surface.GoalState{Objective: "Ship", Status: "active"})
	if strings.Contains(output, "Created:") || strings.Contains(output, "Updated:") || strings.Contains(output, " / ") {
		t.Fatalf("absent budget or timestamps invented: %s", output)
	}
}

func TestGoalControlsAreDiscoverableFromHelp(t *testing.T) {
	output, err := captureStdout(t, func() error { (&App{}).usage(); return nil })
	if err != nil || !strings.Contains(output, "goal <target> [set|edit|pause|resume|budget|clear] [value] [--json]") {
		t.Fatalf("help=%s err=%v", output, err)
	}
}
