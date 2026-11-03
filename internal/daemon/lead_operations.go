package daemon

import (
	"context"
	"net/http"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

func (d *Daemon) projectLeads(context.Context) ([]voice.Project, error) {
	known, err := broadcast.Projects(d.Registry)
	if err != nil {
		return nil, err
	}
	service := d.coordinator()
	list := make([]voice.Project, 0, len(known))
	for _, item := range known {
		specs, err := d.Registry.ProjectCoordinators(item.ID)
		if err != nil {
			return nil, err
		}
		project := voice.Project{Name: item.DisplayName, ID: item.ID, Coordinators: []voice.Lead{}, Conflict: len(specs) > 1}
		for _, spec := range specs {
			lead := voice.Lead{Ref: service.Ref(spec), Host: d.hostIdentity().Name, SessionID: spec.SessionID}
			if spec.HostID != "" {
				lead.Host = service.HostName(spec.HostID)
			} else if id, err := d.Registry.CoordinatorSessionID(spec); err == nil {
				lead.SessionID = id
			}
			project.Coordinators = append(project.Coordinators, lead)
		}
		list = append(list, project)
	}
	return list, nil
}

func (d *Daemon) projectLead(name string) (registry.CoordinatorSpec, error) {
	project, err := broadcast.FindProject(d.Registry, name)
	if err != nil {
		return registry.CoordinatorSpec{}, err
	}
	return d.coordinator().ProjectLead(project.ID, project.DisplayName)
}

func (d *Daemon) doLeadSend(ctx context.Context, _ principal, project, message, sourceSessionID string) (actionResult, error) {
	spec, err := d.projectLead(project)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusConflict, "no_lead", err.Error())
	}
	service := d.coordinator()
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	if spec.HostID != "" {
		receipt, err := daemonBroadcastDelivery{d: d}.PeerCoordinator(ctx, spec, message, sourceSessionID)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusBadGateway, "delivery_failed", err.Error())
		}
		return actionOK(voice.LeadReceipt{Ref: service.Ref(spec), Receipt: receipt}), nil
	}
	outcome, err := service.Deliver(ctx, spec, message, surface.SendOptions{SourceSessionID: sourceSessionID})
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "delivery_failed", err.Error())
	}
	return actionOK(voice.LeadReceipt{Ref: service.Ref(spec), Started: outcome.Started, Receipt: outcome.Receipt}), nil
}
