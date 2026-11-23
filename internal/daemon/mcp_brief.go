package daemon

import (
	"context"

	"github.com/zm2231/agenthail/internal/broadcast"
)

type mcpBriefInput struct {
	mcpWrite
	Project string `json:"project,omitempty" jsonschema:"The project name or id, as agenthail_projects lists it; default the project you work in."`
}

func (d *Daemon) mcpBrief(ctx context.Context, caller *mcpCaller, input mcpBriefInput) (any, error) {
	notice, err := roleGate(caller, "brief", "Asked a lead to brief the person", "A brief is the person's request; write to your lead with agenthail_send.")
	if err != nil {
		return nil, err
	}
	name := input.Project
	if name == "" && caller.project != nil {
		name = caller.project.ID
	}
	project, err := broadcast.FindProject(d.Registry, name)
	if err != nil {
		return nil, refuse("not_found", err.Error())
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_brief", input.mcpWrite, dashboardActionRequest{Action: "coordinator-brief", Project: project.ID, SourceSessionID: caller.session.ID})
	d.noticeAfter(ctx, caller, notice, err)
	return result, err
}
