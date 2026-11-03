package daemon

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpFilterMine       = "mine"
	mcpFilterProject    = "project"
	mcpFilterAllVisible = "all-visible"
)

func (d *Daemon) mcpVisibleSessions(caller *mcpCaller, filter string) ([]surface.Session, error) {
	if filter == "" {
		filter = mcpFilterAllVisible
	}
	if filter != mcpFilterMine && filter != mcpFilterProject && filter != mcpFilterAllVisible {
		return nil, refuse("invalid", fmt.Sprintf("Unknown filter %q; use mine, project or all-visible.", filter))
	}
	descendants := map[string]bool{}
	if caller.has("control.descendants") && filter != mcpFilterProject {
		ids, err := d.Registry.SessionDescendants(caller.session.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			descendants[id] = true
		}
	}
	withProject := caller.has("read.project") && caller.project != nil && filter != mcpFilterMine
	ids, err := d.Registry.LiveLocalSessionIDs()
	if err != nil {
		return nil, err
	}
	visible := []surface.Session{}
	for _, id := range ids {
		self := id == caller.session.ID && caller.has("read.self") && filter == mcpFilterAllVisible
		if !self && !descendants[id] && !withProject {
			continue
		}
		session, err := d.Registry.Session(id)
		if err != nil {
			continue
		}
		if self || descendants[id] || d.sameProject(caller, *session) {
			visible = append(visible, *session)
		}
	}
	return visible, nil
}

func (d *Daemon) sameProject(caller *mcpCaller, session surface.Session) bool {
	project, err := d.sessionProject(session)
	return err == nil && project != nil && caller.project != nil && project.ID == caller.project.ID
}

func (d *Daemon) mcpReadableSession(caller *mcpCaller, ref string) (*surface.Session, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, refuse("invalid", "sessionId is required.")
	}
	id := ref
	if alias, err := d.Registry.LookupAlias(strings.TrimPrefix(ref, "@")); err == nil {
		id = alias
	}
	visible, err := d.mcpVisibleSessions(caller, mcpFilterAllVisible)
	if err != nil {
		return nil, err
	}
	for index := range visible {
		if visible[index].ID == id {
			return &visible[index], nil
		}
	}
	if _, err := d.Registry.Session(id); err != nil {
		return nil, refuse("not_found", fmt.Sprintf("No session %s.", ref))
	}
	return nil, refuse("out_of_scope", fmt.Sprintf("Session %s is not yours, not one you spawned, and not in your project.", ref))
}

func decodeOffsetCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, refuse("invalid", "The cursor is invalid; use the nextCursor a previous call returned.")
	}
	return offset, nil
}

func pageLimit(requested, fallback, max int) (int, error) {
	if requested == 0 {
		return fallback, nil
	}
	if requested < 1 || requested > max {
		return 0, refuse("invalid", fmt.Sprintf("limit must be between 1 and %d.", max))
	}
	return requested, nil
}
