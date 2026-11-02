package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func goalStopped(status string) bool {
	switch status {
	case surface.GoalStatusBlocked, surface.GoalStatusUsageLimited, surface.GoalStatusBudgetLimited:
		return true
	}
	return false
}

func goalKeyOf(goal *surface.GoalState) string {
	key := goal.CreatedAt.UTC().Format(time.RFC3339Nano) + ":" + goal.Objective
	if goalStopped(goal.Status) {
		key += "|" + goal.Status + "|" + goal.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return key
}

func goalStopNote(target string, goal *surface.GoalState) string {
	state := map[string]string{
		surface.GoalStatusBlocked:       "is blocked",
		surface.GoalStatusUsageLimited:  "stopped at a usage limit",
		surface.GoalStatusBudgetLimited: "ran out of its token budget",
	}[goal.Status]
	note := fmt.Sprintf("The goal %q %s", goal.Objective, state)
	if reason := strings.TrimSpace(goal.Reason); reason != "" {
		note += ": " + reason
	}
	note += fmt.Sprintf(". Resume it with: agenthail goal %s resume", target)
	if goal.Status == surface.GoalStatusBudgetLimited {
		note += fmt.Sprintf(", after raising the budget with: agenthail goal %s budget <tokens>", target)
	}
	return note + "."
}
