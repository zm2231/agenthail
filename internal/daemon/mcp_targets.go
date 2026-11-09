package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/targets"
)

func (d *Daemon) mcpTargets(caller *mcpCaller) targets.Resolver[*surface.Session] {
	return targets.Resolver[*surface.Session]{
		Registry:     d.Registry,
		Coordinators: d.coordinator(),
		CallerLead: func() (registry.CoordinatorSpec, error) {
			if caller.project == nil {
				return registry.CoordinatorSpec{}, errors.New("your session is in no project")
			}
			return d.coordinator().ProjectLead(caller.project.ID, caller.project.RootPath)
		},
		Local: func(_ context.Context, target string) (*surface.Session, error) {
			return d.mcpResolveSession(target)
		},
		LocalMissing: func(err error) bool {
			var refusal *mcpRefusal
			return errors.As(err, &refusal) && refusal.Code == "not_found"
		},
		LocalLabel: func(session *surface.Session) string { return d.resolveDisplay(session.ID) },
		Remote: func(ctx context.Context, hostID, target string) ([]targets.Remote, error) {
			candidates := d.pairedHosts()
			if hostID != "" {
				host, ok := d.pairedHost(hostID)
				if !ok {
					return nil, nil
				}
				candidates = []*pairedHost{host}
			}
			answer := d.resolveRemote(ctx, candidates, target)
			if len(answer.Errors) > 0 {
				return nil, fmt.Errorf("ambiguous target %q: %s", target, strings.Join(answer.Errors, "; "))
			}
			matches := make([]targets.Remote, 0, len(answer.Matches))
			for _, match := range answer.Matches {
				matches = append(matches, targets.Remote{ID: match.ID, Surface: match.Surface, Name: match.Name, Alias: match.Alias, Host: targets.Host(match.Host), Exact: match.Exact, Project: match.Project, Lead: match.Lead})
			}
			return matches, nil
		},
	}
}

func mcpTargetRefusal(err error) error {
	var refusal *mcpRefusal
	var missing targets.MissingError
	var ambiguous targets.AmbiguousError
	switch {
	case errors.As(err, &refusal):
		return refusal
	case errors.As(err, &missing):
		return refuse("not_found", missing.Message)
	case errors.As(err, &ambiguous):
		return refuse("invalid", ambiguous.Message)
	}
	return err
}

// mcpRemoteTarget admits a session on a paired host for a queued send only:
// any with control.all, else one in the caller's project that is its
// coordinator or, with control.project, any of them. The project and the
// coordinator role come from the owning host's own answer, never from this
// host's mirror alone, which can be stale.
func (d *Daemon) mcpRemoteTarget(caller *mcpCaller, ref string, remote targets.Remote, upward bool) (*surface.Session, error) {
	if !remote.Exact {
		return nil, refuse("invalid", fmt.Sprintf("%s does not name one session exactly; it matched %s. Address it by @alias or session id.", ref, remote.Label()))
	}
	if !upward {
		return nil, scopeRefusal(remote.ID, "not_queued", fmt.Sprintf("%s runs on host %s; across hosts you may only send a queued message.", remote.Label(), remote.Host.Name))
	}
	if caller.has("control.all") {
		return remote.Session(), nil
	}
	if remote.Project == "" {
		return nil, &mcpRefusal{Code: "unavailable", Retryable: true, Message: fmt.Sprintf("Host %s did not confirm which project %s is in; it may be unreachable or run an older Agenthail. Try again once it answers.", remote.Host.Name, remote.Label()), Detail: map[string]any{"rule": "project_unconfirmed"}}
	}
	if caller.project == nil || remote.Project != caller.project.ID {
		return nil, scopeRefusal(remote.ID, "different_project", fmt.Sprintf("%s runs on host %s and is not in your project; across hosts you may message only sessions in your own project.", remote.Label(), remote.Host.Name))
	}
	if !caller.has("control.project") && !(remote.Lead && d.isCallerLead(caller, remote.ID)) {
		return nil, scopeRefusal(remote.ID, "not_your_lead", fmt.Sprintf("%s runs on host %s; in your project you may message its coordinator, and other sessions only with control.project.", remote.Label(), remote.Host.Name))
	}
	return remote.Session(), nil
}

func (d *Daemon) isCallerLead(caller *mcpCaller, hostSessionID string) bool {
	spec, err := d.coordinator().ProjectLead(caller.project.ID, caller.project.RootPath)
	return err == nil && spec.HostID != "" && spec.SessionID != "" && hosts.Qualify(spec.HostID, spec.SessionID) == hostSessionID
}

func hostProjectID(raw json.RawMessage) string {
	var project struct {
		ID string `json:"id"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &project) != nil {
		return ""
	}
	return project.ID
}
