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

// A coordinator that has never started a conversation leads nothing yet, so the spawner hears instead.
func (s Service) PromptHearer(sessionID, projectID, where string) (string, string, bool) {
	if lead := s.leadOf(sessionID, projectID, where); lead != "" {
		return lead, "lead", true
	}
	if starter := s.starterOf(sessionID); starter != "" {
		return starter, "spawner", true
	}
	return "", "", false
}

// Oversees reports whether sourceID may steer sessionID mid-turn: it is the
// session's lead or the session that started it.
func (s Service) Oversees(sourceID, sessionID, projectID, where string) bool {
	if sourceID == "" || sourceID == sessionID {
		return false
	}
	return sourceID == s.starterOf(sessionID) || sourceID == s.leadOf(sessionID, projectID, where)
}

func (s Service) leadOf(sessionID, projectID, where string) string {
	if _, coordinates := s.Of(sessionID); coordinates || projectID == "" {
		return ""
	}
	spec, err := s.ProjectLead(projectID, where)
	if err != nil {
		return ""
	}
	if lead := s.LeadSession(spec); lead != sessionID {
		return lead
	}
	return ""
}

func (s Service) starterOf(sessionID string) string {
	origin, found, err := s.Registry.SessionOrigin(sessionID)
	if err != nil || !found || origin.CreatorID == sessionID {
		return ""
	}
	return origin.CreatorID
}

func (s Service) LeadSession(spec registry.CoordinatorSpec) string {
	if spec.HostID == "" {
		id, err := s.Registry.CoordinatorSessionID(spec)
		if err != nil {
			return ""
		}
		return id
	}
	id := spec.SessionID
	if host, target, ok := hosts.Split(id); ok {
		if host != spec.HostID {
			return ""
		}
		id = target
	}
	if owner, err := s.Registry.SessionHost(id); err != nil || id == "" || owner != spec.HostID {
		return ""
	}
	return id
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
