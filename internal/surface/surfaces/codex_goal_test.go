package surfaces

import (
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexGoalSetParamsUsesTypedNullableProtocolFields(t *testing.T) {
	objective := "verify release"
	status := surface.GoalStatusPaused
	budget := int64(50000)
	params, err := codexGoalSetParams("thread", surface.GoalUpdate{Objective: &objective, Status: &status, TokenBudget: &budget})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"threadId": "thread", "objective": objective, "status": status, "tokenBudget": budget}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("params=%#v want=%#v", params, want)
	}
	params, err = codexGoalSetParams("thread", surface.GoalUpdate{Objective: &objective})
	if err != nil || params["status"] != nil {
		t.Fatalf("edit params=%#v err=%v", params, err)
	}
	params, err = codexGoalSetParams("thread", surface.GoalUpdate{ClearTokenBudget: true})
	if err != nil || params["tokenBudget"] != nil {
		t.Fatalf("clear params=%#v err=%v", params, err)
	}
}

func TestCodexGoalSetParamsRejectsUnknownStatusAndNegativeBudget(t *testing.T) {
	status := "running"
	if _, err := codexGoalSetParams("thread", surface.GoalUpdate{Status: &status}); err == nil {
		t.Fatal("unknown status accepted")
	}
	budget := int64(-1)
	if _, err := codexGoalSetParams("thread", surface.GoalUpdate{TokenBudget: &budget}); err == nil {
		t.Fatal("negative budget accepted")
	}
}

func TestParseCodexGoalNotificationsPreservesUpdatedAndCleared(t *testing.T) {
	updated, ok := ParseCodexGoalNotification("thread/goal/updated", map[string]any{
		"threadId": "thread", "turnId": "turn",
		"goal": map[string]any{"objective": "verify", "status": "budgetLimited", "timeUsedSeconds": float64(12), "tokensUsed": float64(34), "tokenBudget": float64(55), "createdAt": float64(100), "updatedAt": float64(110)},
	})
	if !ok || updated.Goal == nil || updated.Goal.Status != surface.GoalStatusBudgetLimited || updated.Goal.TimeUsedSeconds != 12 || updated.Goal.TokensUsed != 34 || *updated.Goal.TokenBudget != 55 || !updated.Goal.CreatedAt.Equal(time.Unix(100, 0).UTC()) || updated.TurnID == nil || *updated.TurnID != "turn" {
		t.Fatalf("updated=%+v ok=%v", updated, ok)
	}
	cleared, ok := ParseCodexGoalNotification("thread/goal/cleared", map[string]any{"threadId": "thread"})
	if !ok || cleared.Goal != nil || cleared.ThreadID != "thread" {
		t.Fatalf("cleared=%+v ok=%v", cleared, ok)
	}
}

func TestCodexGoalNotificationsBecomeStreamEvents(t *testing.T) {
	event, ok := codexGoalStreamEvent(codexEvent{Sequence: 9, Method: "thread/goal/cleared", Params: map[string]any{"threadId": "thread"}})
	if !ok || event.Kind != "goal" || event.Operation != "replace" || event.Version != 9 || event.Goal != nil {
		t.Fatalf("event=%+v ok=%v", event, ok)
	}
	if _, ok := codexGoalStreamEvent(codexEvent{Method: "thread/status/changed", Params: map[string]any{"threadId": "thread"}}); ok {
		t.Fatal("non-goal notification was emitted as a goal event")
	}
}
