package daemon

import (
	"context"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/registry"
)

type projectEntry struct {
	Name         string      `json:"name"`
	ID           string      `json:"id"`
	Coordinators []leadEntry `json:"coordinators"`
	Conflict     bool        `json:"conflict,omitempty"`
}

type leadEntry struct {
	Ref       string `json:"ref"`
	Host      string `json:"host"`
	SessionID string `json:"sessionId,omitempty"`
}

func (d *Daemon) projectLeads(context.Context) ([]projectEntry, error) {
	known, err := broadcast.Projects(d.Registry)
	if err != nil {
		return nil, err
	}
	service := d.coordinator()
	list := make([]projectEntry, 0, len(known))
	for _, item := range known {
		specs, err := d.Registry.ProjectCoordinators(item.ID)
		if err != nil {
			return nil, err
		}
		project := projectEntry{Name: item.DisplayName, ID: item.ID, Coordinators: []leadEntry{}, Conflict: len(specs) > 1}
		for _, spec := range specs {
			lead := leadEntry{Ref: service.Ref(spec), Host: d.hostIdentity().Name, SessionID: spec.SessionID}
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
