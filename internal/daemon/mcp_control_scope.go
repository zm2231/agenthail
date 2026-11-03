package daemon

import (
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
func (d *Daemon) mcpControlTarget(caller *mcpCaller, ref string, upward bool) (*surface.Session, error) {
	session, err := d.mcpResolveSession(ref)
	if err != nil {
		return nil, err
	}
	if session.ID == caller.session.ID {
		return nil, refuse("invalid", "That is your own session.")
	}
	if host, err := d.Registry.SessionHost(session.ID); err != nil || host != "" {
		return nil, refuse("out_of_scope", "That session runs on another host; reach a project's coordinator there by sending with project instead.")
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
			return session, nil
		}
	}
	if caller.has("control.project") && d.sameProject(caller, *session) {
		return session, nil
	}
	if upward {
		above, err := d.mcpAbove(caller, session.ID)
		if err != nil || above {
			return session, err
		}
	}
	if d.mcpIsAbove(caller, session.ID) {
		return nil, refuse("out_of_scope", "Control flows down only: you may send a queued message to your spawner or your project's coordinator, but not steer, interrupt, change its goal or send now.")
	}
	return nil, refuse("out_of_scope", "That session is not one you spawned or, as its project's coordinator, lead.")
}

func (d *Daemon) mcpAbove(caller *mcpCaller, sessionID string) (bool, error) {
	ancestors, err := d.Registry.SessionAncestors(caller.session.ID)
	if err != nil {
		return false, err
	}
	if slices.Contains(ancestors, sessionID) {
		return true, nil
	}
	if caller.project == nil {
		return false, nil
	}
	spec, err := d.Registry.Coordinator(caller.project.ID)
	if err != nil || spec.HostID != "" {
		return false, nil
	}
	lead, err := d.Registry.CoordinatorSessionID(spec)
	return err == nil && lead == sessionID, nil
}

func (d *Daemon) mcpIsAbove(caller *mcpCaller, sessionID string) bool {
	above, err := d.mcpAbove(caller, sessionID)
	return err == nil && above
}
