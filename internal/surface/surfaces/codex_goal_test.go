package surfaces

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestParseCodexGoalNotificationsPreservesUpdatedAndCleared(t *testing.T) {
	updated, ok := ParseCodexGoalNotification("thread/goal/updated", map[string]any{
		"threadId": "thread", "turnId": "turn",
		"goal": map[string]any{"objective": "verify", "status": "budgetLimited", "timeUsedSeconds": float64(12), "tokensUsed": float64(34), "tokenBudget": float64(55), "createdAt": float64(100), "updatedAt": float64(110)},
	})
	if !ok || updated.Goal == nil || updated.Goal.Status != surface.GoalStatusBudgetLimited || updated.Goal.TimeUsedSeconds != 12 || updated.Goal.TokensUsed != 34 || *updated.Goal.TokenBudget != 55 || !updated.Goal.CreatedAt.Equal(time.Unix(100, 0).UTC()) || !updated.Goal.UpdatedAt.Equal(time.Unix(110, 0).UTC()) || updated.TurnID == nil || *updated.TurnID != "turn" {
		t.Fatalf("updated=%+v ok=%v", updated, ok)
	}
	cleared, ok := ParseCodexGoalNotification("thread/goal/cleared", map[string]any{"threadId": "thread"})
	if !ok || cleared.Goal != nil || cleared.ThreadID != "thread" {
		t.Fatalf("cleared=%+v ok=%v", cleared, ok)
	}
}

func TestCodexUpdateGoalSendsTypedNullableProtocolFields(t *testing.T) {
	fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/resume" {
			return map[string]any{"thread": map[string]any{"id": "thread"}}
		}
		return nil
	})
	codex := isolatedManagedRuntime(t)
	objective := "verify release"
	status := surface.GoalStatusPaused
	budget := int64(50000)
	for _, update := range []surface.GoalUpdate{
		{Objective: &objective, Status: &status, TokenBudget: &budget},
		{Objective: &objective},
		{ClearTokenBudget: true},
	} {
		if err := codex.UpdateGoal(context.Background(), managedSession(), update); err != nil {
			t.Fatal(err)
		}
	}
	sets := fake.Calls("thread/goal/set")
	if len(sets) != 3 {
		t.Fatalf("methods=%v", fake.Methods())
	}
	want := map[string]any{"threadId": "thread", "objective": objective, "status": status, "tokenBudget": float64(budget)}
	if !reflect.DeepEqual(sets[0], want) {
		t.Fatalf("params=%#v want=%#v", sets[0], want)
	}
	if _, present := sets[1]["status"]; present {
		t.Fatalf("objective edit changed status: %#v", sets[1])
	}
	if value, present := sets[2]["tokenBudget"]; !present || value != nil {
		t.Fatalf("budget clear was not an explicit null: %#v", sets[2])
	}
}

func TestCodexUpdateGoalRejectsUnknownStatusAndNegativeBudgetBeforeDelivery(t *testing.T) {
	fake := startManagedCodex(t, func(string, map[string]any) map[string]any { return nil })
	codex := isolatedManagedRuntime(t)
	status := "running"
	budget := int64(-1)
	for _, update := range []surface.GoalUpdate{{Status: &status}, {TokenBudget: &budget}, {}} {
		if err := codex.UpdateGoal(context.Background(), managedSession(), update); err == nil {
			t.Fatalf("invalid goal update accepted: %+v", update)
		}
	}
	if methods := fake.Methods(); len(methods) != 0 {
		t.Fatalf("invalid goal updates reached the app-server: %v", methods)
	}
}
