package cli

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/workspace"
)

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

func (a *App) scopeProject(ctx context.Context, projects daemon.SessionProjects, selector, cwd string) (string, error) {
	if selector != "" {
		return projects.Named(selector)
	}
	folder, err := workspace.NormalizeCWD(cwd)
	if err != nil {
		return "", fmt.Errorf("normalize --cwd: %w", err)
	}
	return daemon.ProjectHolding(ctx, a.registry(), folder)
}
