package daemon

import "context"

type mcpInterruptInput struct {
	mcpWrite
	Target string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead."`
}

func (d *Daemon) mcpInterrupt(ctx context.Context, caller *mcpCaller, input mcpInterruptInput) (any, error) {
	target, err := d.mcpControlTarget(ctx, caller, input.Target, false)
	if err != nil {
		return nil, err
	}
	return d.mcpAction(ctx, caller, "agenthail_interrupt", input.mcpWrite, dashboardActionRequest{Action: "interrupt", SessionID: target.ID})
}
