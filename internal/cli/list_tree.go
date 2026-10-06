package cli

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type listRow struct {
	session surface.Session
	depth   int
}

// The limit counts families, not threads.
func listFamilies(sessions []surface.Session, limit int) []listRow {
	byID := make(map[string]surface.Session, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	children := map[string][]surface.Session{}
	var roots []surface.Session
	for _, session := range sessions {
		if session.Subagent != nil {
			if _, found := byID[session.Subagent.ParentID]; found && session.Subagent.ParentID != session.ID {
				children[session.Subagent.ParentID] = append(children[session.Subagent.ParentID], session)
				continue
			}
		}
		roots = append(roots, session)
	}
	if limit >= 0 && len(roots) > limit {
		roots = roots[:limit]
	}
	rows := make([]listRow, 0, len(sessions))
	visited := map[string]bool{}
	var walk func(surface.Session, int)
	walk = func(session surface.Session, depth int) {
		if visited[session.ID] {
			return
		}
		visited[session.ID] = true
		rows = append(rows, listRow{session: session, depth: depth})
		for _, child := range children[session.ID] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
}

func listRollups(sessions []surface.Session) map[string]surface.SubagentRollup {
	byID := make(map[string]surface.Session, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}
	rollups := map[string]surface.SubagentRollup{}
	for _, session := range sessions {
		if session.Subagents != nil {
			rollup := rollups[session.ID]
			rollup.Count += session.Subagents.Count
			rollup.Working += session.Subagents.Working
			rollups[session.ID] = rollup
		}
		seen := map[string]bool{session.ID: true}
		for current := session; current.Subagent != nil; {
			parent, found := byID[current.Subagent.ParentID]
			if !found || seen[parent.ID] {
				break
			}
			seen[parent.ID] = true
			rollup := rollups[parent.ID]
			rollup.Count++
			if session.Status == surface.StatusBusy {
				rollup.Working++
			}
			rollups[parent.ID] = rollup
			current = parent
		}
	}
	return rollups
}

func listRowName(row listRow) string {
	if row.depth > 0 && row.session.Subagent.Nickname != "" {
		return row.session.Subagent.Nickname
	}
	return row.session.Name
}

func catalogSubagentRollup(record registry.CatalogSessionState) *surface.SubagentRollup {
	var projection struct {
		Subagents *surface.SubagentRollup `json:"subagents"`
	}
	if json.Unmarshal([]byte(record.ProjectionFingerprint), &projection) != nil {
		return nil
	}
	return projection.Subagents
}
