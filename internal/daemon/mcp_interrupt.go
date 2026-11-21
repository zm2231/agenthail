package daemon

import "context"

type mcpInterruptInput struct {
	mcpWrite
	Target string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
	mcpAskPerson
}

func (d *Daemon) mcpInterrupt(ctx context.Context, caller *mcpCaller, input mcpInterruptInput) (any, error) {
	target, notice, err := d.mcpReachTarget(ctx, caller, input.Target, reachSteer)
	if err != nil {
		return d.mcpAskOnRefusal(ctx, caller, err, input.mcpAskPerson, dashboardActionRequest{Action: "interrupt"})
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_interrupt", input.mcpWrite, dashboardActionRequest{Action: "interrupt", SessionID: target.ID})
	d.noticeAfter(ctx, caller, notice, err)
	return result, err
}
