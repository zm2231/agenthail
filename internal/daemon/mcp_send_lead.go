package daemon

import (
	"context"

	"github.com/zm2231/agenthail/internal/broadcast"
)

func (d *Daemon) mcpSendToLead(ctx context.Context, caller *mcpCaller, input mcpSendInput) (any, error) {
	project, err := broadcast.FindProject(d.Registry, input.Project)
	if err != nil {
		return nil, refuse("not_found", err.Error())
	}
	own := caller.project != nil && caller.project.ID == project.ID
	if !caller.has("control.all") && !own {
		return nil, refuse("out_of_scope", "You may message only your own project's coordinator.")
	}
	if own && !caller.has("control.all") && input.Mode == "now" {
		return nil, refuse("out_of_scope", "Control flows down only: send your coordinator a queued message.")
	}
	return d.mcpAction(ctx, caller, "agenthail_send", input.mcpWrite, dashboardActionRequest{Action: "coordinator-send", Project: project.ID, Message: input.Message, SourceSessionID: caller.session.ID})
}
