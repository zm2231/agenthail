package daemon

import (
	"context"
	"strings"
)

type mcpSteerInput struct {
	mcpWrite
	Target  string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
	Message string `json:"message"`
	mcpAskPerson
}

func (d *Daemon) mcpSteer(ctx context.Context, caller *mcpCaller, input mcpSteerInput) (any, error) {
	if strings.TrimSpace(input.Message) == "" {
		return nil, refuse("invalid", "message is required.")
	}
	target, err := d.mcpControlTarget(ctx, caller, input.Target, false)
	if err != nil {
		return d.mcpAskOnRefusal(ctx, caller, err, input.mcpAskPerson, dashboardActionRequest{Action: "steer", Message: input.Message})
	}
	return d.mcpAction(ctx, caller, "agenthail_steer", input.mcpWrite, dashboardActionRequest{Action: "steer", SessionID: target.ID, Message: input.Message, SourceSessionID: caller.session.ID})
}
