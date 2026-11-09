package daemon

import (
	"context"
	"slices"
	"strings"

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

// mcpControlTarget resolves a target the caller may act on; upward admits the caller's spawner chain and project coordinator, which only queued sends use.
func (d *Daemon) mcpControlTarget(ctx context.Context, caller *mcpCaller, ref string, upward bool) (*surface.Session, error) {
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
	if host, err := d.Registry.SessionHost(session.ID); err != nil || host != "" {
		return nil, refuse("out_of_scope", "That session runs on another host; reach a project's coordinator there by sending with project instead.")
	}
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return nil, err
	}
	if slices.Contains(ancestors, session.ID) {
		if upward {
			return session, nil
		}
		return nil, refuse("out_of_scope", mcpDownOnly)
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
		return nil, refuse("out_of_scope", mcpDownOnly)
	}
	return nil, refuse("out_of_scope", "That session is not one you spawned or, as its project's coordinator, lead; ask your project's coordinator to reach it.")
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
		return &mcpRefusal{Code: "exceeds_ceiling", Message: "That session may do more than you may, so you cannot control it.", Detail: map[string]any{"axis": "target", "limit": limit}}
	}
	return nil
}
