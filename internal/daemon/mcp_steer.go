package daemon

import (
	"context"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
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
	target, notice, err := d.mcpReachTarget(ctx, caller, input.Target, reachSteer)
	if err != nil {
		return d.mcpAskOnRefusal(ctx, caller, err, input.mcpAskPerson, dashboardActionRequest{Action: "steer", Message: input.Message})
	}
	ctx = surface.WithSteerAdmitted(ctx, caller.session.ID)
	result, err := d.mcpAction(ctx, caller, "agenthail_steer", input.mcpWrite, dashboardActionRequest{Action: "steer", SessionID: target.ID, Message: input.Message, SourceSessionID: caller.session.ID})
	d.noticeAfter(ctx, caller, notice.told(nil, ""), err)
	return result, err
}
