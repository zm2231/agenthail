package daemon

import (
	"context"
	"errors"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/dashconfig"
)

func (d *Daemon) mcpSendToLead(ctx context.Context, caller *mcpCaller, input mcpSendInput) (any, error) {
	project, notice, refusal := d.mcpLeadScope(caller, input.Project, input.Mode)
	if refusal != nil {
		return nil, refusal
	}
	result, err := d.mcpAction(ctx, caller, "agenthail_send", input.mcpWrite, dashboardActionRequest{Action: "coordinator-send", Project: project.ID, Message: input.Message, SourceSessionID: caller.session.ID})
	d.noticeAfter(ctx, caller, notice, err)
	return result, err
}

// mcpLeadScope admits a message to a project's coordinator. Sent now to
// another project's, it is a steer, which the person's cross-project-steer
// decides; the notice is what to tell the person once it has run.
func (d *Daemon) mcpLeadScope(caller *mcpCaller, name, mode string) (broadcast.Project, string, *mcpRefusal) {
	project, err := broadcast.FindProject(d.Registry, name)
	if err != nil {
		return broadcast.Project{}, "", refuse("not_found", err.Error())
	}
	own := caller.project != nil && caller.project.ID == project.ID
	if caller.has("control.all") || mode != "now" {
		return project, "", nil
	}
	if own {
		return broadcast.Project{}, "", scopeRefusal("project:"+project.ID, "down_only", "Control flows down only: send your coordinator a queued message.")
	}
	refused := scopeRefusal("project:"+project.ID, "different_project", "Another project's coordinator takes only a queued message from you.")
	decision := d.agentPolicy().Decision("cross-project-steer")
	if caller.extension != "" || decision == dashconfig.AgentAsk {
		return broadcast.Project{}, "", refused
	}
	if lead, found := d.projectLeadSession(project.ID); found {
		if err := d.mcpCeilingCovers(caller, lead); err != nil {
			var refusal *mcpRefusal
			if errors.As(err, &refusal) {
				return broadcast.Project{}, "", refusal
			}
			return broadcast.Project{}, "", refused
		}
	}
	if decision == dashconfig.AgentNotify {
		return project, "Sent now to the coordinator of " + project.DisplayName + ", another project", nil
	}
	return project, "", nil
}

func (d *Daemon) projectLeadSession(projectID string) (string, bool) {
	spec, err := d.Registry.Coordinator(projectID)
	if err != nil || spec.HostID != "" {
		return "", false
	}
	lead, err := d.Registry.CoordinatorSessionID(spec)
	return lead, err == nil && lead != ""
}
