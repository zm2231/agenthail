package daemon

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

type voiceProjects struct{ d *Daemon }

func (p voiceProjects) List(context.Context) ([]voice.Project, error) {
	known, err := broadcast.Projects(p.d.Registry)
	if err != nil {
		return nil, err
	}
	service := p.d.coordinator()
	list := make([]voice.Project, 0, len(known))
	for _, item := range known {
		specs, err := p.d.Registry.ProjectCoordinators(item.ID)
		if err != nil {
			return nil, err
		}
		project := voice.Project{Name: item.DisplayName, ID: item.ID, Coordinators: []voice.Lead{}, Conflict: len(specs) > 1}
		for _, spec := range specs {
			lead := voice.Lead{Ref: service.Ref(spec), Host: p.d.hostIdentity().Name, SessionID: spec.SessionID}
			if spec.HostID != "" {
				lead.Host = service.HostName(spec.HostID)
			} else if id, err := p.d.Registry.CoordinatorSessionID(spec); err == nil {
				lead.SessionID = id
			}
			project.Coordinators = append(project.Coordinators, lead)
		}
		list = append(list, project)
	}
	return list, nil
}

func (p voiceProjects) lead(name string) (registry.CoordinatorSpec, error) {
	project, err := broadcast.FindProject(p.d.Registry, name)
	if err != nil {
		return registry.CoordinatorSpec{}, err
	}
	return p.d.coordinator().ProjectLead(project.ID, project.DisplayName)
}

func (p voiceProjects) LeadSession(_ context.Context, name string) (string, error) {
	spec, err := p.lead(name)
	if err != nil {
		return "", err
	}
	service := p.d.coordinator()
	if spec.HostID != "" {
		return "", fmt.Errorf("%s runs on host %s; voice can only be transferred to sessions on this host. Send it a message instead", service.Ref(spec), service.HostName(spec.HostID))
	}
	id, err := p.d.Registry.CoordinatorSessionID(spec)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("%s has no conversation yet; send it a message first, which starts one", service.Ref(spec))
	}
	return id, nil
}

func (p voiceProjects) SendToLead(ctx context.Context, name, message, sourceSessionID string) (voice.LeadReceipt, error) {
	spec, err := p.lead(name)
	if err != nil {
		return voice.LeadReceipt{}, err
	}
	service := p.d.coordinator()
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	if spec.HostID != "" {
		receipt, err := daemonBroadcastDelivery{d: p.d}.PeerCoordinator(ctx, spec, message, sourceSessionID)
		if err != nil {
			return voice.LeadReceipt{}, err
		}
		return voice.LeadReceipt{Ref: service.Ref(spec), Receipt: receipt}, nil
	}
	outcome, err := service.Deliver(ctx, spec, message, surface.SendOptions{SourceSessionID: sourceSessionID})
	if err != nil {
		return voice.LeadReceipt{}, err
	}
	return voice.LeadReceipt{Ref: service.Ref(spec), Started: outcome.Started, Receipt: outcome.Receipt}, nil
}
