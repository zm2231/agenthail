package daemon

import (
	"context"
	"strings"

	"github.com/zm2231/agenthail/internal/broadcast"
)

type mcpCoordinatorInput struct {
	mcpWrite
	Action  string `json:"action" jsonschema:"set: make target its project's coordinator. clear: the project has no coordinator."`
	Target  string `json:"target,omitempty" jsonschema:"For set: the session id or @alias to lead its project."`
	Name    string `json:"name,omitempty" jsonschema:"For set: the coordinator's name, reached as @name; default lead-<project>."`
	Replace bool   `json:"replace,omitempty" jsonschema:"For set: replace a coordinator the project already has."`
	Project string `json:"project,omitempty" jsonschema:"For clear: the project name or id, as agenthail_projects lists it."`
}

func (d *Daemon) mcpCoordinator(ctx context.Context, caller *mcpCaller, input mcpCoordinatorInput) (any, error) {
	notice, err := roleGate(caller, "coordinator-any", "Changed a project's coordinator", "Choosing a project's coordinator is for the person.")
	if err != nil {
		return nil, err
	}
	var request dashboardActionRequest
	switch strings.TrimSpace(input.Action) {
	case "set":
		target, err := d.mcpResolveSession(input.Target)
		if err != nil {
			return nil, err
		}
		request = dashboardActionRequest{Action: "coordinator-set", SessionID: target.ID, Name: strings.TrimPrefix(strings.TrimSpace(input.Name), "@"), Replace: input.Replace}
	case "clear":
		project, err := broadcast.FindProject(d.Registry, input.Project)
		if err != nil {
			return nil, refuse("not_found", err.Error())
		}
		request = dashboardActionRequest{Action: "coordinator-clear", ProjectID: project.ID}
	default:
		return nil, refuse("invalid", "action is set or clear.")
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_coordinator", input.mcpWrite, request)
	d.noticeAfter(ctx, caller, notice, err)
	return result, err
}
