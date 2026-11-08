package daemon

import (
	"context"
	"strconv"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
)

const mcpPageBudget = mcpOutputLimit - 1<<10

type mcpSessionsInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"mine (sessions you spawned), project (sessions in your project) or all-visible (the default)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"at most this many sessions, 1 to 50; default 20"`
	Cursor string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call"`
}

type mcpSessionView struct {
	ID         string          `json:"id"`
	Alias      string          `json:"alias,omitempty"`
	Name       string          `json:"name,omitempty"`
	Surface    string          `json:"surface"`
	Status     string          `json:"status"`
	Cwd        string          `json:"cwd"`
	Project    *mcpProjectView `json:"project"`
	LastActive string          `json:"lastActive,omitempty"`
}

func (d *Daemon) mcpSessions(_ context.Context, caller *mcpCaller, input mcpSessionsInput) (any, error) {
	limit, err := pageLimit(input.Limit, 20, 50)
	if err != nil {
		return nil, err
	}
	after, err := decodeOrderCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	all, err := d.mcpVisibleSessions(caller, input.Filter)
	if err != nil {
		return nil, err
	}
	visible := visibleAfter(all, after)
	views := []mcpSessionView{}
	used := 0
	next := 0
	for ; next < len(visible) && len(views) < limit; next++ {
		session := visible[next].Session
		alias, _ := d.Registry.ReverseAlias(session.ID)
		project, _ := d.sessionProject(session)
		view := mcpSessionView{ID: session.ID, Alias: alias, Name: session.Name, Surface: string(session.Surface), Status: string(session.Status), Cwd: session.Cwd, Project: projectView(project)}
		if !session.LastActive.IsZero() {
			view.LastActive = session.LastActive.UTC().Format(time.RFC3339)
		}
		encoded, _ := apitime.Marshal(view)
		if used+len(encoded) > mcpPageBudget && len(views) > 0 {
			break
		}
		used += len(encoded)
		views = append(views, view)
	}
	result := map[string]any{"sessions": views, "total": len(all), "truncated": next < len(visible)}
	if next < len(visible) {
		result["nextCursor"] = strconv.FormatInt(visible[next-1].order, 10)
	}
	return result, nil
}
