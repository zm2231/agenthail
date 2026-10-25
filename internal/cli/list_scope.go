package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/workspace"
)

type listProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
}

type listCatalogView struct {
	Project *listProject `json:"project,omitempty"`
}

func catalogListView(record registry.CatalogSessionState) listCatalogView {
	var view listCatalogView
	_ = json.Unmarshal([]byte(record.ProjectionFingerprint), &view)
	return view
}

func listScopeFromArgs(args []string) (string, error) {
	scope := ""
	for _, candidate := range []string{"all", "archived", "recent", "running"} {
		if !hasFlag(args, "--"+candidate) {
			continue
		}
		if scope != "" {
			return "", fmt.Errorf("choose one of --all, --archived, --recent, or --running")
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
