package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type CodexGoalNotification struct {
	Method   string
	ThreadID string
	TurnID   *string
	Goal     *surface.GoalState
}

func codexGoalStreamEvent(event codexEvent) (surface.StreamEvent, bool) {
	notification, ok := ParseCodexGoalNotification(event.Method, event.Params)
	if !ok {
		return surface.StreamEvent{}, false
	}
	return surface.StreamEvent{ID: fmt.Sprintf("goal:%d", event.Sequence), Version: uint64(event.Sequence), Operation: "replace", TurnID: stringValue(notification.TurnID), Kind: "goal", Goal: notification.Goal}, true
}

func ParseCodexGoalNotification(method string, params map[string]any) (CodexGoalNotification, bool) {
	if method != "thread/goal/updated" && method != "thread/goal/cleared" {
		return CodexGoalNotification{}, false
	}
	notification := CodexGoalNotification{Method: method, ThreadID: str(params, "threadId")}
	if turnID, ok := params["turnId"].(string); ok {
		notification.TurnID = &turnID
	}
	if method == "thread/goal/updated" {
		goal, _ := params["goal"].(map[string]any)
		if goal == nil {
			return CodexGoalNotification{}, false
		}
		notification.Goal = decodeCodexGoal(goal)
	}
	return notification, notification.ThreadID != ""
}

func (c *Codex) UpdateGoal(ctx context.Context, sess *surface.Session, update surface.GoalUpdate) error {
	if sess == nil || sess.ID == "" {
		return fmt.Errorf("Codex goal update requires a session")
	}
	params, err := codexGoalSetParams(sess.ID, update)
	if err != nil {
		return err
	}
	_, err = c.requestSession(ctx, sess, true, "thread/goal/set", params, 5*time.Second)
	return err
}

func codexGoalSetParams(threadID string, update surface.GoalUpdate) (map[string]any, error) {
	params := map[string]any{"threadId": threadID}
	if update.Objective != nil {
		params["objective"] = *update.Objective
	}
	if update.Status != nil {
		if !validCodexGoalStatus(*update.Status) {
			return nil, fmt.Errorf("unsupported Codex goal status %q", *update.Status)
		}
		params["status"] = *update.Status
	}
	if update.ClearTokenBudget {
		params["tokenBudget"] = nil
	} else if update.TokenBudget != nil {
		if *update.TokenBudget < 0 {
			return nil, fmt.Errorf("Codex goal token budget cannot be negative")
		}
		params["tokenBudget"] = *update.TokenBudget
	}
	if len(params) == 1 {
		return nil, fmt.Errorf("Codex goal update is empty")
	}
	return params, nil
}

func (c *Codex) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	return c.UpdateGoal(ctx, sess, surface.GoalUpdate{Objective: &text, Status: stringPointer(surface.GoalStatusActive)})
}

func (c *Codex) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	resp, err := c.requestSession(ctx, sess, false, "thread/goal/get", map[string]any{"threadId": sess.ID}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]any)
	goal, _ := result["goal"].(map[string]any)
	if goal == nil {
		return nil, nil
	}
	return decodeCodexGoal(goal), nil
}

func decodeCodexGoal(goal map[string]any) *surface.GoalState {
	state := &surface.GoalState{
		Objective:       str(goal, "objective"),
		Status:          str(goal, "status"),
		TimeUsedSeconds: int64Value(goal["timeUsedSeconds"]),
		TokensUsed:      int64Value(goal["tokensUsed"]),
		CreatedAt:       codexUnixTime(goal["createdAt"]),
		UpdatedAt:       codexUnixTime(goal["updatedAt"]),
	}
	if goal["tokenBudget"] != nil {
		value := int64Value(goal["tokenBudget"])
		state.TokenBudget = &value
	}
	return state
}

func validCodexGoalStatus(status string) bool {
	switch status {
	case surface.GoalStatusActive, surface.GoalStatusPaused, surface.GoalStatusBlocked, surface.GoalStatusUsageLimited, surface.GoalStatusBudgetLimited, surface.GoalStatusComplete:
		return true
	default:
		return false
	}
}

func stringPointer(value string) *string { return &value }

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int64Value(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case int:
		return int64(number)
	case float64:
		return int64(number)
	case json.Number:
		parsed, _ := number.Int64()
		return parsed
	default:
		return 0
	}
}

func codexUnixTime(value any) time.Time {
	seconds := int64Value(value)
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
