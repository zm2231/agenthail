package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
				matches = append(matches, targets.Remote{ID: match.ID, Surface: match.Surface, Name: match.Name, Alias: match.Alias, Host: targets.Host(match.Host), Exact: match.Exact, Project: match.Project, Archived: match.Archived})
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
// any with control.all or in the caller's spawner chain, else a non-archived
// one in the caller's project. The project and the archive come from the
// owning host's own answer, never from this host's mirror alone, which can be
// stale; while that host does not answer, the send queues and the owner
// checks the project itself (remoteSendScope).
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
	spawner, err := d.remoteSpawner(caller, remote.ID)
	if err != nil || spawner {
		return remote.Session(), err
	}
	if remote.Project == "" && caller.project != nil {
		return remote.Session(), nil
	}
	if caller.project == nil || remote.Project != caller.project.ID {
		return nil, scopeRefusal(remote.ID, "different_project", fmt.Sprintf("%s runs on host %s and is not in your project; across hosts you may message only sessions in your own project.", remote.Label(), remote.Host.Name))
	}
	if remote.Archived {
		return nil, scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	return remote.Session(), nil
}

func (d *Daemon) remoteSpawner(caller *mcpCaller, ref string) (bool, error) {
	_, id, ok := hosts.Split(ref)
	if !ok {
		return false, nil
	}
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return false, err
	}
	return slices.Contains(ancestors, id), nil
}

// remoteSendScope names the project the owning host must find a remote
// target in before it delivers a send, for a caller whose reach across hosts
// is its own project.
func (d *Daemon) remoteSendScope(caller *mcpCaller, target *surface.Session) (string, error) {
	if _, _, remote := hosts.Split(target.ID); !remote || caller.has("control.all") || caller.project == nil {
		return "", nil
	}
	spawner, err := d.remoteSpawner(caller, target.ID)
	if err != nil || spawner {
		return "", err
	}
	return caller.project.ID, nil
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
