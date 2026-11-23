package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/daemonapi"
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
			if len(answer.Ambiguous) > 0 {
				return nil, targets.AmbiguousError{Message: fmt.Sprintf("ambiguous target %q: %s", target, strings.Join(answer.Ambiguous, "; "))}
			}
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

// mcpRemoteTarget admits a session on a paired host. A queued send goes to
// any with control.all or in the caller's spawner chain, else a non-archived
// one in the caller's project. Anything more reaches only a session in the
// caller's own project, for a caller with project reach whose role's
// remote-project-control is not ask. The project and the archive come from
// the owning host's own answer, never from this host's mirror alone, which
// can be stale, and the owner checks them again (forwardedScope); while that
// host does not answer, a send queues.
func (d *Daemon) mcpRemoteTarget(caller *mcpCaller, ref string, remote targets.Remote, reach controlReach) (*surface.Session, error) {
	if !remote.Exact {
		return nil, refuse("invalid", fmt.Sprintf("%s does not name one session exactly; it matched %s. Address it by @alias or session id.", ref, remote.Label()))
	}
	if reach != reachQueued {
		spawner, err := d.remoteSpawner(caller, remote.ID)
		if err != nil {
			return nil, err
		}
		if spawner {
			return nil, scopeRefusal(remote.ID, "down_only", mcpDownOnly)
		}
		return d.remoteProjectControl(caller, remote, reach)
	}
	if caller.has("control.all") || remote.Surface == surface.KindAgent {
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
		return nil, crossProject(scopeRefusal(remote.ID, "different_project", fmt.Sprintf("%s runs on host %s and is not in your project; across hosts you may message only sessions in your own project.", remote.Label(), remote.Host.Name)))
	}
	if remote.Archived {
		return nil, scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	return remote.Session(), nil
}

// A coordinator oversees every session in its project, so this host knows
// which decision its steer, send now or interrupt falls under. Whether the
// caller started the session only the owning host knows; that host picks
// between the two decisions forwardScope forwards.
func (d *Daemon) remoteProjectControl(caller *mcpCaller, remote targets.Remote, reach controlReach) (*surface.Session, error) {
	queuedOnly := scopeRefusal(remote.ID, "not_queued", fmt.Sprintf("%s runs on host %s; across hosts you may only send a queued message, unless it is in your own project and you are its coordinator.", remote.Label(), remote.Host.Name))
	if caller.extension != "" || !caller.has("control.project") || caller.project == nil || remote.Surface == surface.KindAgent || remote.Project != caller.project.ID {
		return nil, queuedOnly
	}
	if remote.Archived {
		return nil, scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	if reach == reachSteer && d.mcpIsProjectLead(caller, caller.session.ID) {
		if caller.role.Decision(overseenControl) == agentroles.Ask {
			return nil, overseenRefusal(remote.ID, remote.Label())
		}
		return remote.Session(), nil
	}
	if caller.role.Decision(remoteProjectControlAction) != agentroles.Ask {
		return remote.Session(), nil
	}
	return nil, scopeRefusal(remote.ID, "not_queued", fmt.Sprintf("%s runs on host %s; your role leaves controlling sessions on another computer to the person, so you may only send it a queued message.", remote.Label(), remote.Host.Name))
}

const (
	overseenControl            = "overseen-control"
	remoteProjectControlAction = "remote-project-control"
)

func overseenRefusal(targetID, label string) error {
	return scopeRefusal(targetID, "not_queued", fmt.Sprintf("Your role leaves steering, interrupting or sending now to %s, a session you started or lead, to the person, so you may only send it a queued message.", label))
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

type forwardedLimits struct {
	project string
	rank    string
	// overseen and control are the caller's decisions for a session it
	// started or leads and for any other session in its project; the owning
	// host knows which applies and reports the one it used.
	overseen string
	control  string
	notice   agentAction
}

// forwardScope names what the owning host checks before it acts on a remote
// target for a caller whose reach across hosts is its own project: the
// project, and for anything more than a queued send the caller's permission
// rank. Its notice is what to tell the person once the action has run.
func (d *Daemon) forwardScope(caller *mcpCaller, targetID, action string, reach controlReach) (forwardedLimits, error) {
	if _, id, remote := hosts.Split(targetID); !remote || caller.project == nil || surface.IsAgentIdentityID(id) {
		return forwardedLimits{}, nil
	}
	if reach == reachQueued {
		if caller.has("control.all") {
			return forwardedLimits{}, nil
		}
		spawner, err := d.remoteSpawner(caller, targetID)
		if err != nil || spawner {
			return forwardedLimits{}, err
		}
		return forwardedLimits{project: caller.project.ID}, nil
	}
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return forwardedLimits{}, err
	}
	limits := forwardedLimits{project: caller.project.ID, rank: ceiling.Permission}
	if caller.role == nil {
		return limits, nil
	}
	limits.overseen, limits.control = caller.role.Decision(overseenControl), caller.role.Decision(remoteProjectControlAction)
	if reach == reachSteer && d.mcpIsProjectLead(caller, caller.session.ID) {
		limits.control = limits.overseen
	}
	if limits.overseen == agentroles.Notify || limits.control == agentroles.Notify {
		limits.notice = d.remoteControlNotice(targetID, action)
	}
	return limits, nil
}

func (d *Daemon) remoteControlNotice(targetID, action string) agentAction {
	host, id, _ := hosts.Split(targetID)
	where := "another computer"
	if paired, found := d.pairedHost(host); found {
		where = paired.record.Name
	}
	target := d.resolveDisplay(id) + " on " + where
	if matches, err := d.Registry.MatchHostSessions(host, id); err == nil && len(matches) == 1 && matches[0].Alias != "" {
		target = "@" + matches[0].Alias + " on " + where
	}
	return remoteActionWords(action, target)
}

func remoteActionWords(action, target string) agentAction {
	switch action {
	case "steer":
		return agentAction{
			done:       "Steered " + target,
			notSteered: "Sent " + target + " a message (that computer does not know this session as its lead, so it could not steer)",
			unknown:    "Sent " + target + " a steer (that computer runs an older Agenthail, so it may have arrived as a message)",
		}
	case "send":
		return agentAction{done: "Sent " + target + " a message"}
	case "interrupt":
		return agentAction{done: "Interrupted " + target}
	case "":
		return agentAction{done: "Controlled " + target, target: target}
	}
	return agentAction{done: "Ran " + action + " on " + target}
}

// scopeReply hands the CLI what it attaches to the action it then posts, and
// holds the notice the CLI sends once that action has run.
func (d *Daemon) scopeReply(caller *mcpCaller, reply *daemonapi.ScopeReply, targetID, action string, reach controlReach) error {
	limits, err := d.forwardScope(caller, targetID, action, reach)
	if err != nil {
		return err
	}
	reply.ScopeProjectID, reply.ScopeRank = limits.project, limits.rank
	reply.ScopeOverseen, reply.ScopeControl = limits.overseen, limits.control
	if reply.Notice == "" {
		reply.Notice = d.holdAgentAction(caller, limits.notice)
	}
	return nil
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
