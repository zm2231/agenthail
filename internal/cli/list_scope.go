package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

type listProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
	Kind        string `json:"kind"`
	Archived    bool   `json:"archived,omitempty"`
}

type listCatalogView struct {
	Project          *listProject               `json:"project,omitempty"`
	ClaudeConfigRoot *surface.SessionConfigRoot `json:"claudeConfigRoot,omitempty"`
	Subagents        *surface.SubagentRollup    `json:"-"`
	Holders          []surface.Holder           `json:"-"`
	Current          bool                       `json:"current"`
	Archived         bool                       `json:"archived,omitempty"`
}

func catalogListView(record registry.CatalogSessionState) listCatalogView {
	var projection struct {
		listCatalogView
		Subagents *surface.SubagentRollup `json:"subagents"`
		Holders   []surface.Holder        `json:"holders"`
	}
	_ = json.Unmarshal([]byte(record.ProjectionFingerprint), &projection)
	view := projection.listCatalogView
	view.Subagents, view.Holders = projection.Subagents, projection.Holders
	return view
}

func listScopeFromArgs(args []string) (string, error) {
	scope := ""
	for _, candidate := range []string{"all", "archived"} {
		if !hasFlag(args, "--"+candidate) {
			continue
		}
		if scope != "" {
			return "", fmt.Errorf("choose either --all or --archived")
		}
		scope = candidate
	}
	if scope == "" {
		scope = "current"
	}
	return scope, nil
}

// resolveListProject turns a project id or display name into one project id.
// A name shared by several projects is an error, never a pick.
func resolveListProject(views map[string]listCatalogView, selector string) (string, error) {
	named := map[string]*listProject{}
	for _, view := range views {
		if view.Project == nil {
			continue
		}
		if view.Project.ID == selector {
			return selector, nil
		}
		if strings.EqualFold(view.Project.DisplayName, selector) {
			named[view.Project.ID] = view.Project
		}
	}
	switch len(named) {
	case 0:
		return "", fmt.Errorf("no project matched %q; see 'agenthail project list --all'", selector)
	case 1:
		for id := range named {
			return id, nil
		}
	}
	labels := make([]string, 0, len(named))
	for id, project := range named {
		labels = append(labels, fmt.Sprintf("%s (%s)", project.RootPath, id))
	}
	sort.Strings(labels)
	return "", fmt.Errorf("project %q is ambiguous: %s; use its id", selector, strings.Join(labels, ", "))
}

// filterListScope keeps the session families with a member in scope and, when
// projectID is set, in that project. A subagent belongs to its root's family
// when the root is listed.
func filterListScope(sessions []surface.Session, views map[string]listCatalogView, scope, projectID string) []surface.Session {
	listed := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		listed[session.ID] = true
	}
	family := func(session surface.Session) string {
		if session.Subagent != nil && session.Subagent.RootID != "" && listed[session.Subagent.RootID] {
			return session.Subagent.RootID
		}
		return session.ID
	}
	matched := map[string]bool{}
	for _, session := range sessions {
		view := views[session.ID]
		if projectID != "" && (view.Project == nil || view.Project.ID != projectID) {
			continue
		}
		if daemon.ScopeMatches(scope, daemon.SessionWorking(session.Status, view.Subagents), view.Current, view.Archived) {
			matched[family(session)] = true
		}
	}
	kept := sessions[:0]
	for _, session := range sessions {
		if matched[family(session)] {
			kept = append(kept, session)
		}
	}
	return kept
}

func listProjectColumn(view listCatalogView) string {
	if view.Project == nil {
		return "-"
	}
	return view.Project.DisplayName
}

// selectedProject is the project --project names, or the project whose
// sessions a session working in --cwd would join, so a worktree session
// counts for the project its folder belongs to. It is "" when neither is set.
func (a *App) selectedProject(ctx context.Context, views map[string]listCatalogView, selector, cwd string) (string, error) {
	if selector != "" {
		return resolveListProject(views, selector)
	}
	if cwd == "" {
		return "", nil
	}
	folder, err := workspace.NormalizeCWD(cwd)
	if err != nil {
		return "", fmt.Errorf("normalize --cwd: %w", err)
	}
	found, err := daemon.ResolvePathProject(ctx, a.registry(), folder)
	if err != nil {
		return "", err
	}
	if found == nil {
		return "", fmt.Errorf("no project holds %s; add it with 'agenthail project add %s'", folder, folder)
	}
	return found.ID, nil
}
