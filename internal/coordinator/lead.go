package coordinator

import (
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

// ProjectLead returns the one coordinator of a project, on any paired host.
// Coordinators of one project on two hosts are a conflict to resolve, never
// a pick. where names the project in the refusals.
func (s Service) ProjectLead(projectID, where string) (registry.CoordinatorSpec, error) {
	specs, err := s.Registry.ProjectCoordinators(projectID)
	if err != nil {
		return registry.CoordinatorSpec{}, err
	}
	switch len(specs) {
	case 0:
		return registry.CoordinatorSpec{}, fmt.Errorf("%s has no coordinator; use 'agenthail coordinator new' or 'agenthail coordinator set'", where)
	case 1:
		return specs[0], nil
	}
	return registry.CoordinatorSpec{}, fmt.Errorf("%s has coordinators %s; resolve it with 'agenthail coordinator set --replace' on one host", where, s.Refs(specs))
}

// Ref is how a person names a coordinator: @name on this host, host::@name
// on a paired host.
func (s Service) Ref(spec registry.CoordinatorSpec) string {
	if identity, err := s.Registry.HostIdentity(); spec.HostID == "" || err == nil && spec.HostID == identity.ID {
		return "@" + spec.Name
	}
	return s.HostName(spec.HostID) + hosts.Separator + "@" + spec.Name
}

func (s Service) Refs(specs []registry.CoordinatorSpec) string {
	refs := make([]string, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, s.Ref(spec))
	}
	return strings.Join(refs, " and ")
}

func (s Service) HostName(hostID string) string {
	if host, err := s.Registry.Host(hostID); err == nil && host.Name != "" {
		return host.Name
	}
	return hostID
}
