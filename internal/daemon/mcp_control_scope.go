package daemon

import (
	"context"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) mcpResolveSession(ref string) (*surface.Session, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, refuse("invalid", "target is required: a session id or @alias.")
	}
	id := ref
	if alias, err := d.Registry.LookupAlias(strings.TrimPrefix(ref, "@")); err == nil {
		id = alias
	}
	session, err := d.Registry.Session(id)
	if err != nil {
		return nil, refuse("not_found", "No session "+ref+".")
	}
	return session, nil
}

// controlReach is what the caller does to the target; a queued message
// alone may go up to the caller's spawner chain and to any session in its
// project.
type controlReach int

const (
	reachQueued controlReach = iota
	reachSteer
	reachSession
)

func (d *Daemon) mcpControlTarget(ctx context.Context, caller *mcpCaller, ref string) (*surface.Session, error) {
	session, _, err := d.mcpReachTarget(ctx, caller, ref, reachSession)
	return session, err
}

// mcpReachTarget resolves a target the caller may message or steer. A
// target in another project the person's policy admits comes with what to
// tell the person once the action has run.
func (d *Daemon) mcpReachTarget(ctx context.Context, caller *mcpCaller, ref string, reach controlReach) (*surface.Session, string, error) {
	session, err := d.reachTarget(ctx, caller, ref, reach)
	if _, refusedAcross := approvableControl(err); !refusedAcross || session == nil {
		if err != nil {
			return nil, "", err
		}
		return session, "", nil
	}
	notice, admitted, crossErr := d.crossProjectReach(caller, session, reach)
	if crossErr != nil {
		return nil, "", crossErr
	}
	if !admitted {
		return nil, "", err
	}
	return session, notice, nil
}

func (d *Daemon) reachTarget(ctx context.Context, caller *mcpCaller, ref string, reach controlReach) (*surface.Session, error) {
	upward := reach == reachQueued
	if strings.TrimSpace(ref) == "" {
		return nil, refuse("invalid", "target is required: a session id or @alias.")
	}
	session, remote, err := d.mcpTargets(caller).Resolve(ctx, strings.TrimSpace(ref))
	if err != nil {
		return nil, mcpTargetRefusal(err)
	}
	if remote != nil {
		return d.mcpRemoteTarget(caller, ref, *remote, upward)
	}
	if session.ID == caller.session.ID {
		return nil, refuse("invalid", "That is your own session.")
	}
	if surface.IsAgentIdentity(session) && upward {
		return session, nil
	}
	if host, err := d.Registry.SessionHost(session.ID); err != nil || host != "" {
		return nil, scopeRefusal(session.ID, "other_host", "That session runs on another host; reach a project's coordinator there by sending with project instead.")
	}
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return nil, err
	}
	if slices.Contains(ancestors, session.ID) {
		if upward {
			return session, nil
		}
		return nil, scopeRefusal(session.ID, "down_only", mcpDownOnly)
	}
	if caller.has("control.all") {
		return session, nil
	}
	if caller.has("control.descendants") {
		descendants, err := d.Registry.SessionDescendants(caller.session.ID)
		if err != nil {
			return nil, err
		}
		if slices.Contains(descendants, session.ID) {
			return session, d.mcpCeilingCovers(caller, session.ID)
		}
	}
	if caller.has("control.project") && d.sameProject(caller, *session) {
		return session, d.mcpCeilingCovers(caller, session.ID)
	}
	if d.mcpIsProjectLead(caller, session.ID) {
		if upward {
			return session, nil
		}
		return nil, scopeRefusal(session.ID, "down_only", mcpDownOnly)
	}
	if d.sameProject(caller, *session) {
		if !upward {
			return nil, scopeRefusal(session.ID, "not_spawned_or_led", "That session is in your project but is not one you spawned or, as its project's coordinator, lead; you may send it a queued message, nothing more.")
		}
		archived, err := d.Registry.SessionArchived(session.ID)
		if err != nil {
			return nil, err
		}
		if archived {
			return nil, scopeRefusal(session.ID, "archived", "That session is archived.")
		}
		return session, nil
	}
	return session, crossProject(scopeRefusal(session.ID, "not_spawned_or_led", "That session is not in your project; reach another project through its coordinator, by sending with project."))
}

func (d *Daemon) crossProjectReach(caller *mcpCaller, session *surface.Session, reach controlReach) (string, bool, error) {
	action, what := "cross-project-message", "Sent a message to "
	switch {
	case caller.extension != "" || reach == reachSession:
		return "", false, nil
	case reach == reachSteer:
		if surface.IsAgentIdentity(session) {
			return "", false, nil
		}
		action, what = "cross-project-steer", "Steered, interrupted or sent now to "
	}
	decision := d.agentPolicy().Decision(action)
	if decision == dashconfig.AgentAsk {
		return "", false, nil
	}
	if reach == reachSteer {
		if err := d.mcpCeilingCovers(caller, session.ID); err != nil {
			return "", false, err
		}
	} else if archived, err := d.Registry.SessionArchived(session.ID); err != nil || archived {
		if err == nil {
			err = scopeRefusal(session.ID, "archived", "That session is archived.")
		}
		return "", false, err
	}
	if decision == dashconfig.AgentNotify {
		return what + d.resolveDisplay(session.ID) + " in another project", true, nil
	}
	return "", true, nil
}

const mcpDownOnly = "Control flows down only: you may send a queued message to your spawner or your project's coordinator, but not steer, interrupt, answer, change its goal or settings, or send now."

func (d *Daemon) mcpIsProjectLead(caller *mcpCaller, sessionID string) bool {
	if caller.project == nil {
		return false
	}
	spec, err := d.Registry.Coordinator(caller.project.ID)
	if err != nil || spec.HostID != "" {
		return false
	}
	lead, err := d.Registry.CoordinatorSessionID(spec)
	return err == nil && lead == sessionID
}

func (d *Daemon) mcpCeilingCovers(caller *mcpCaller, targetID string) error {
	limit, err := parseCeiling(caller.ceiling)
	if err != nil {
		return err
	}
	raw, err := d.Registry.SessionCeiling(targetID)
	if err != nil {
		return err
	}
	target, err := parseCeiling(raw)
	if err != nil {
		return err
	}
	if !target.within(limit) {
		return (&mcpRefusal{Code: "exceeds_ceiling", Message: "That session may do more than you may, so you cannot control it.", Detail: map[string]any{"axis": "target", "limit": limit}}).about(targetID)
	}
	return nil
}
