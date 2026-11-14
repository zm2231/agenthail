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
	if !caller.has("control.all") {
		return nil, refuse("capability_missing", "A brief is the person's request, for the voice operator; write to your lead with agenthail_send.")
	}
	name := input.Project
	if name == "" && caller.project != nil {
		name = caller.project.ID
	}
	project, err := broadcast.FindProject(d.Registry, name)
	if err != nil {
		return nil, refuse("not_found", err.Error())
	}
	return d.mcpAction(ctx, caller, "agenthail_brief", input.mcpWrite, dashboardActionRequest{Action: "coordinator-brief", Project: project.ID, SourceSessionID: caller.session.ID})
}
