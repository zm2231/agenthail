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
				return nil, fmt.Errorf("resolve target %q: %s", target, strings.Join(answer.Errors, "; "))
			}
			matches := make([]targets.Remote, 0, len(answer.Matches))
			for _, match := range answer.Matches {
				matches = append(matches, targets.Remote{ID: match.ID, Surface: match.Surface, Name: match.Name, Alias: match.Alias, Host: targets.Host(match.Host), Exact: match.Exact, Project: match.Project, Archived: match.Archived, StartedBy: match.StartedBy})
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
// any with control.all, the caller's starter or a session the caller started
// (starter_scope.go), else a non-archived one in the caller's project.
// Anything more reaches a session the caller started, or one in its own
// project for a caller with project reach whose role's remote-project-control
// is not ask. The project, the archive and the starters come from the owning
// host's own answer, never from this host's mirror alone, which can be stale,
// and the owner checks them again (forwardedScope); while that host does not
// answer, a send queues.
func (d *Daemon) mcpRemoteTarget(caller *mcpCaller, ref string, remote targets.Remote, reach controlReach) (*surface.Session, error) {
	if !remote.Exact {
		return nil, refuse("invalid", fmt.Sprintf("%s does not name one session exactly; it matched %s. Address it by @alias or session id.", ref, remote.Label()))
	}
	tie, err := d.remoteStarterTie(caller, remote)
	if err != nil {
		return nil, err
	}
	if reach != reachQueued {
		switch tie {
		case tieStartedCaller:
			return nil, scopeRefusal(remote.ID, "down_only", mcpDownOnly)
		case tieCallerStarted:
			return remoteStarterControl(caller, remote, reach)
		}
		return d.remoteProjectControl(caller, remote, reach)
	}
	if caller.has("control.all") || remote.Surface == surface.KindAgent || tie != untied {
		return remote.Session(), nil
	}
	if remote.Project == "" && caller.project != nil {
		return remote.Session(), nil
	}
	if caller.project == nil || remote.Project != caller.project.ID {
		return nil, crossProject(scopeRefusal(remote.ID, "different_project", fmt.Sprintf("%s runs on host %s and is not in your project; across hosts you may message only sessions in your own project, the session that started you and sessions you started.", remote.Label(), remote.Host.Name)))
	}
	if remote.Archived {
		return nil, scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	return remote.Session(), nil
}

// remoteStarterControl admits a session the caller started on a paired host
// as a session it started on this one: with control.descendants, under its
// role's decision for sessions it started or leads, wherever it runs and
// whatever project either is in.
func remoteStarterControl(caller *mcpCaller, remote targets.Remote, reach controlReach) (*surface.Session, error) {
	if caller.extension != "" || !caller.has("control.descendants") {
		return nil, scopeRefusal(remote.ID, "not_queued", fmt.Sprintf("%s runs on host %s; you may only send it a queued message.", remote.Label(), remote.Host.Name))
	}
	if remote.Archived {
		return nil, scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	if caller.role.Decision(overseenControl) == agentroles.Ask {
		return nil, overseenRefusal(remote.ID, remote.Label(), reach)
	}
	return remote.Session(), nil
}

// A coordinator oversees every session in its project, so this host knows
// its overseen-control decides. Whether any other caller started the
// session only the owning host knows, so this host admits it when either
// decision might, and that host picks between the two forwardScope forwards.
func (d *Daemon) remoteProjectControl(caller *mcpCaller, remote targets.Remote, reach controlReach) (*surface.Session, error) {
	queuedOnly := scopeRefusal(remote.ID, "not_queued", fmt.Sprintf("%s runs on host %s; across hosts you may only send a queued message, unless it is in your own project and you are its coordinator.", remote.Label(), remote.Host.Name))
	if caller.extension != "" || !caller.has("control.project") || caller.project == nil || remote.Surface == surface.KindAgent || remote.Project != caller.project.ID {
		return nil, queuedOnly
	}
	if remote.Archived {
		return nil, scopeRefusal(remote.ID, "archived", fmt.Sprintf("%s on host %s is archived.", remote.Label(), remote.Host.Name))
	}
	overseen := caller.role.Decision(overseenControl)
	if d.mcpIsProjectLead(caller, caller.session.ID) {
		if overseen == agentroles.Ask {
			return nil, overseenRefusal(remote.ID, remote.Label(), reach)
		}
		return remote.Session(), nil
	}
	if overseen != agentroles.Ask || caller.role.Decision(remoteProjectControlAction) != agentroles.Ask {
		return remote.Session(), nil
	}
	return nil, scopeRefusal(remote.ID, "not_queued", fmt.Sprintf("%s runs on host %s; your role leaves controlling sessions on another computer to the person, so you may only send it a queued message.", remote.Label(), remote.Host.Name))
}

const (
	overseenControl            = "overseen-control"
	remoteProjectControlAction = "remote-project-control"
)

func overseenRefusal(targetID, label string, reach controlReach) error {
	what := "steering, interrupting or sending now to"
	if reach == reachSession {
		what = "changing"
	}
	return scopeRefusal(targetID, "not_queued", fmt.Sprintf("Your role leaves %s %s, a session you started or lead, to the person, so you may only send it a queued message.", what, label))
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
// target for an agent: the caller's project, which the session must share
// unless the caller started it, and for anything more than a queued send the
// caller's permission rank. Its notice is what to tell the person once the
// action has run. Only this host knows the session that started the caller,
// so a queued send to it goes unchecked there.
func (d *Daemon) forwardScope(caller *mcpCaller, targetID, action string, reach controlReach) (forwardedLimits, error) {
	if _, id, remote := hosts.Split(targetID); !remote || surface.IsAgentIdentityID(id) {
		return forwardedLimits{}, nil
	}
	project := ""
	if caller.project != nil {
		project = caller.project.ID
	}
	if reach == reachQueued {
		if caller.has("control.all") {
			return forwardedLimits{}, nil
		}
		chain, err := d.starterChain(caller.session.ID)
		if err != nil || slices.Contains(chain, targetID) {
			return forwardedLimits{}, err
		}
		return forwardedLimits{project: project}, nil
	}
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return forwardedLimits{}, err
	}
	limits := forwardedLimits{project: project, rank: ceiling.Permission}
	if caller.role == nil {
		return limits, nil
	}
	limits.overseen, limits.control = caller.role.Decision(overseenControl), caller.role.Decision(remoteProjectControlAction)
	if d.mcpIsProjectLead(caller, caller.session.ID) {
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
	return actionWords(action, target)
}

var sessionActionWords = map[string]string{
	"session-archive":   "Archived ",
	"session-unarchive": "Restored ",
	"alias":             "Renamed ",
	"session-pin":       "Pinned ",
	"session-unpin":     "Unpinned ",
	"session-snooze":    "Snoozed ",
	"session-wake":      "Woke ",
	"goal-set":          "Set the goal of ",
	"goal-edit":         "Edited the goal of ",
	"goal-clear":        "Cleared the goal of ",
	"goal-pause":        "Paused the goal of ",
	"goal-resume":       "Resumed the goal of ",
	"goal-budget":       "Set the goal budget of ",
	"model":             "Changed the model of ",
	"permission-mode":   "Changed the permission mode of ",
	"effort":            "Changed the effort of ",
	"compact":           "Compacted ",
	"pr":                "Changed the pull requests of ",
}

func actionWords(action, target string) agentAction {
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
	if verb, found := sessionActionWords[action]; found {
		return agentAction{done: verb + target}
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
