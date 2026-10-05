package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestGoalReportsStatusUsageAndAttention(t *testing.T) {
	budget := int64(1000)
	at := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	fake := &cliSurface{
		kind:     surface.KindCodex,
		sessions: map[string]surface.Session{"g": {ID: "g", Surface: surface.KindCodex}},
		caps:     surface.Capabilities{Goal: true},
	}
	app, _ := cliFixture(t, fake)
	show := func(goal *surface.GoalState, flags ...string) string {
		t.Helper()
		fake.goal = goal
		output, err := captureStdout(t, func() error { return app.Run(append([]string{"goal", "codex:g"}, flags...)) })
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	full := func(status string) *surface.GoalState {
		return &surface.GoalState{Objective: "Ship", Status: status, TimeUsedSeconds: 72, TokensUsed: 480, TokenBudget: &budget, CreatedAt: at, UpdatedAt: at.Add(time.Minute)}
	}
	baseline := strings.Count(show(full(surface.GoalStatusActive)), "\n")
	for _, status := range []string{surface.GoalStatusActive, surface.GoalStatusPaused, surface.GoalStatusBlocked, surface.GoalStatusUsageLimited, surface.GoalStatusBudgetLimited, surface.GoalStatusComplete} {
		output := show(full(status))
		for _, value := range []string{"Ship", status, "72", "480", "1000", "2026-10-04T08:00:00Z", "2026-10-04T08:01:00Z"} {
			if !strings.Contains(output, value) {
				t.Fatalf("status=%s missing %q in %s", status, value, output)
			}
		}
		needsAttention := status == surface.GoalStatusBlocked || status == surface.GoalStatusUsageLimited || status == surface.GoalStatusBudgetLimited
		if flagged := strings.Count(output, "\n") > baseline; flagged != needsAttention {
			t.Fatalf("status=%s attention flagged=%v output=%s", status, flagged, output)
		}
	}
	sparse := show(&surface.GoalState{Objective: "Ship", Status: surface.GoalStatusActive})
	if strings.Contains(sparse, "1000") || strings.Contains(sparse, "2026-") {
		t.Fatalf("absent budget or timestamps invented: %s", sparse)
	}
	var document struct {
		Session string            `json:"session"`
		Goal    surface.GoalState `json:"goal"`
	}
	if err := json.Unmarshal([]byte(show(full(surface.GoalStatusBlocked), "--json")), &document); err != nil {
		t.Fatal(err)
	}
	if document.Session != "g" || document.Goal.Status != surface.GoalStatusBlocked || document.Goal.TokenBudget == nil || *document.Goal.TokenBudget != budget || !document.Goal.CreatedAt.Equal(at) {
		t.Fatalf("document=%+v", document)
	}
}
