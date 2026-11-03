package daemon

import (
	"context"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type mcpGoalInput struct {
	mcpWrite
	Target string `json:"target" jsonschema:"A Codex session id or @alias you spawned or, as coordinator, lead."`
	Action string `json:"action" jsonschema:"set, edit, clear, pause, resume or budget."`
	Text   string `json:"text,omitempty" jsonschema:"The goal for set and edit; the token budget for budget (empty clears it)."`
}

var mcpGoalActions = map[string]string{"set": "goal-set", "edit": "goal-edit", "clear": "goal-clear", "pause": "goal-pause", "resume": "goal-resume", "budget": "goal-budget"}

func (d *Daemon) mcpGoal(ctx context.Context, caller *mcpCaller, input mcpGoalInput) (any, error) {
	action, ok := mcpGoalActions[strings.TrimSpace(input.Action)]
	if !ok {
		return nil, refuse("invalid", "action is set, edit, clear, pause, resume or budget.")
	}
	if (action == "goal-set" || action == "goal-edit") && strings.TrimSpace(input.Text) == "" {
		return nil, refuse("invalid", "text is required to set or edit a goal.")
	}
	target, err := d.mcpControlTarget(caller, input.Target, false)
	if err != nil {
		return nil, err
	}
	if target.Surface != surface.KindCodex {
		return nil, refuse("unsupported_surface", "Goals are a Codex feature; that session is "+string(target.Surface)+".")
	}
	return d.mcpAction(ctx, caller, "agenthail_goal", input.mcpWrite, dashboardActionRequest{Action: action, SessionID: target.ID, Message: input.Text})
}
