// Package targets resolves a session target on this host or a paired host:
// host::target names the host, and an unqualified target is looked up here
// and on every paired host, where a match on more than one is an error,
// never a pick. The CLI and the daemon's agent tools resolve through it.
package targets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type Host struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
}

// Remote is a session on a paired host, by its host-qualified ID.
type Remote struct {
	ID       string              `json:"id"`
	Surface  surface.SurfaceKind `json:"surface"`
	Name     string              `json:"name"`
	Alias    string              `json:"alias,omitempty"`
	Host     Host                `json:"host"`
	Exact    bool                `json:"exact"`
	Project  string              `json:"project,omitempty"`
	Archived bool                `json:"archived,omitempty"`
}

func (t Remote) Label() string {
	_, raw, _ := hosts.Split(t.ID)
	if t.Alias != "" {
		raw = "@" + t.Alias
	}
	return t.Host.Name + hosts.Separator + raw
}

func (t Remote) Session() *surface.Session {
	return &surface.Session{ID: t.ID, Surface: t.Surface, Name: t.Name}
}

type Resolver[L any] struct {
	Registry     *registry.Registry
	Coordinators coordinator.Service
	// CallerLead is the coordinator of the caller's project, for @lead.
	CallerLead func() (registry.CoordinatorSpec, error)
	// Local resolves a target on this host; LocalMissing says its error only
	// means nothing matched, so paired hosts are asked too.
	Local        func(ctx context.Context, target string) (L, error)
	LocalMissing func(error) bool
	LocalLabel   func(L) string
	// Remote lists the matches on one paired host, or on all of them when
	// hostID is empty.
	Remote func(ctx context.Context, hostID, target string) ([]Remote, error)
}

func (r Resolver[L]) Resolve(ctx context.Context, target string) (L, *Remote, error) {
	var none L
	identity, err := r.Registry.HostIdentity()
	if err != nil {
		return none, nil, fmt.Errorf("read host identity: %w", err)
	}
	if target, err = r.ExpandTargetLead(target, identity); err != nil {
		return none, nil, err
	}
	if id, hostID, found, err := r.Registry.CanonicalSession(target); err != nil {
		return none, nil, err
	} else if found {
		target = hosts.Qualify(hostID, id)
	}
	if hostText, rest, ok := hosts.Split(target); ok {
		if strings.EqualFold(hostText, identity.ID) || strings.EqualFold(hostText, identity.Name) {
			local, err := r.Local(ctx, rest)
			return local, nil, err
		}
		host, err := r.Registry.Host(hostText)
		if errors.Is(err, registry.ErrHostNotFound) {
			return none, nil, MissingError{fmt.Sprintf("no paired host is named %q; see 'agenthail hosts'", hostText)}
		}
		if err != nil {
			return none, nil, err
		}
		matches, err := r.Remote(ctx, host.ID, rest)
		if err != nil {
			return none, nil, err
		}
		switch len(matches) {
		case 0:
			return none, nil, MissingError{fmt.Sprintf("no session matched '%s' on host %s", rest, host.Name)}
		case 1:
			return none, &matches[0], nil
		}
		return none, nil, ambiguous(target, nil, matches)
	}
	paired, err := r.Registry.ListHosts()
	if err != nil {
		return none, nil, fmt.Errorf("list paired hosts: %w", err)
	}
	local, localErr := r.Local(ctx, target)
	if len(paired) == 0 {
		return local, nil, localErr
	}
	if localErr != nil && !r.LocalMissing(localErr) {
		return none, nil, localErr
	}
	matches, err := r.Remote(ctx, "", target)
	if err != nil {
		return none, nil, err
	}
	switch {
	case len(matches) == 0:
		return local, nil, localErr
	case localErr != nil && len(matches) == 1:
		return none, &matches[0], nil
	}
	var labels []string
	if localErr == nil {
		labels = append(labels, identity.Name+hosts.Separator+r.LocalLabel(local))
	}
	return none, nil, ambiguous(target, labels, matches)
}

// MissingError is a target no session on the named host matches, or a host
// that is not paired.
type MissingError struct{ Message string }

func (e MissingError) Error() string { return e.Message }

// AmbiguousError is a target that matches more than one session.
type AmbiguousError struct{ Message string }

func (e AmbiguousError) Error() string { return e.Message }

func ambiguous(target string, local []string, remote []Remote) error {
	labels := append([]string{}, local...)
	for _, match := range remote {
		labels = append(labels, match.Label())
	}
	return AmbiguousError{fmt.Sprintf("ambiguous target %q matched %s; qualify it as host::target", target, strings.Join(labels, ", "))}
}

// ExpandTargetLead expands @lead in an unqualified target or one qualified
// with this host. Qualified with this host it stays on this host, as it
// always resolves here.
func (r Resolver[L]) ExpandTargetLead(target string, identity registry.HostIdentity) (string, error) {
	hostText, rest, qualified := hosts.Split(target)
	if !qualified {
		return r.ExpandLead(target)
	}
	if !strings.EqualFold(hostText, identity.ID) && !strings.EqualFold(hostText, identity.Name) {
		return target, nil
	}
	expanded, err := r.ExpandLead(rest)
	if err != nil {
		return "", err
	}
	return hostText + hosts.Separator + expanded, nil
}

// LeadTarget resolves @lead and a coordinator's @name. A coordinator on
// this host stays a name the session resolvers take; one on a paired host
// comes back as its spec.
func (r Resolver[L]) LeadTarget(target string) (string, *registry.CoordinatorSpec, error) {
	name, ok := strings.CutPrefix(target, "@")
	if !ok || strings.Contains(name, "/") {
		return target, nil, nil
	}
	if strings.EqualFold(name, registry.LeadAlias) {
		spec, err := r.CallerLead()
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", registry.LeadAlias, err)
		}
		if spec.HostID == "" {
			return "@" + spec.Name, nil, nil
		}
		return target, &spec, nil
	}
	if _, err := r.Registry.ExactTarget("", name); err == nil {
		return target, nil, nil
	}
	if _, err := r.Registry.CoordinatorNamed(name); err == nil {
		return target, nil, nil
	}
	named, err := r.Registry.CoordinatorsNamed(name)
	if err != nil {
		return "", nil, err
	}
	switch len(named) {
	case 0:
		return target, nil, nil
	case 1:
		return target, &named[0], nil
	}
	return "", nil, fmt.Errorf("@%s names coordinators on several hosts: %s; send to one as <host>::@%s", name, r.Coordinators.Refs(named), name)
}

// ExpandLead resolves @lead and a paired host's @name to a target the
// session resolvers take: a paired host's coordinator by its conversation.
// @lead/<nickname>[/<nickname>...] walks the coordinator's subagents from its
// conversation ID, so the path's parent is exact.
func (r Resolver[L]) ExpandLead(target string) (string, error) {
	if path, ok := strings.CutPrefix(target, "@"); ok {
		if head, rest, isPath := strings.Cut(path, "/"); isPath && strings.EqualFold(head, registry.LeadAlias) {
			return r.expandLeadPath(rest)
		}
	}
	resolved, remote, err := r.LeadTarget(target)
	if err != nil || remote == nil {
		return resolved, err
	}
	if remote.SessionID == "" {
		return "", fmt.Errorf("%s has no conversation yet; send to it first, which starts one", r.Coordinators.Ref(*remote))
	}
	_, raw, _ := hosts.Split(remote.SessionID)
	return r.Coordinators.HostName(remote.HostID) + hosts.Separator + raw, nil
}

func (r Resolver[L]) expandLeadPath(nicknames string) (string, error) {
	spec, err := r.CallerLead()
	if err != nil {
		return "", fmt.Errorf("@%s: %w", registry.LeadAlias, err)
	}
	if spec.HostID != "" {
		if spec.SessionID == "" {
			return "", fmt.Errorf("%s has no conversation yet; send to it first, which starts one", r.Coordinators.Ref(spec))
		}
		_, raw, _ := hosts.Split(spec.SessionID)
		return r.Coordinators.HostName(spec.HostID) + hosts.Separator + "@" + raw + "/" + nicknames, nil
	}
	id, err := r.Registry.CoordinatorSessionID(spec)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("%s has no conversation yet; send to it first, which starts one", r.Coordinators.Ref(spec))
	}
	return "@" + id + "/" + nicknames, nil
}
