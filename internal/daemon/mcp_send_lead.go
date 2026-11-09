package daemon

import (
	"context"

	"github.com/zm2231/agenthail/internal/broadcast"
)

func (d *Daemon) mcpSendToLead(ctx context.Context, caller *mcpCaller, input mcpSendInput) (any, error) {
	project, refusal := d.mcpLeadScope(caller, input.Project, input.Mode)
	if refusal != nil {
		return nil, refusal
	}
	return d.mcpAction(ctx, caller, "agenthail_send", input.mcpWrite, dashboardActionRequest{Action: "coordinator-send", Project: project.ID, Message: input.Message, SourceSessionID: caller.session.ID})
}

func (d *Daemon) mcpLeadScope(caller *mcpCaller, name, mode string) (broadcast.Project, *mcpRefusal) {
	project, err := broadcast.FindProject(d.Registry, name)
	if err != nil {
		return broadcast.Project{}, refuse("not_found", err.Error())
	}
	own := caller.project != nil && caller.project.ID == project.ID
	if !caller.has("control.all") && mode == "now" {
		if !own {
			return broadcast.Project{}, &mcpRefusal{Code: "out_of_scope", Message: "Another project's coordinator takes only a queued message from you.", Detail: map[string]any{"rule": "different_project"}}
		}
		return broadcast.Project{}, refuse("out_of_scope", "Control flows down only: send your coordinator a queued message.")
	}
	return project, nil
}
