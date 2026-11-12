package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestAGoalHandoffRelaysTheGoalsLastReplyNotTheNudgeAfterIt(t *testing.T) {
	goal := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	events := []registry.RelayEvent{
		{ID: 1, Message: "before the goal", CompletedAt: goal.Add(-time.Minute)},
		{ID: 2, Message: "the goal is done", CompletedAt: goal.Add(time.Minute)},
		{ID: 3, Message: "", CompletedAt: goal.Add(2 * time.Minute)},
	}
	if got := goalReplyIndex(events, goal); got != 1 {
		t.Fatalf("relayed from event %d, want the goal's reply (1) and the nudge reply consumed after it", got)
	}
	if got := goalReplyIndex(events[:1], goal); got != 0 {
		t.Fatalf("with no reply inside the goal, start at the last event; got %d", got)
	}
	if got := goalReplyIndex([]registry.RelayEvent{{ID: 4, Message: "", CompletedAt: goal.Add(time.Minute)}}, goal); got != 0 {
		t.Fatalf("a lone empty event: got %d", got)
	}
}
