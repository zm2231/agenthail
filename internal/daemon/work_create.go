package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/worktree"
)

const isolationWorktree = "worktree"

func wantsWorktree(request dashboardActionRequest) (bool, error) {
	switch strings.TrimSpace(request.Isolation) {
	case "":
		if strings.TrimSpace(request.Base) != "" && strings.TrimSpace(request.Worktree) == "" {
			return false, invalidRequest("base needs isolation worktree")
		}
		return strings.TrimSpace(request.Worktree) != "", nil
	case isolationWorktree:
		return true, nil
	default:
		return false, invalidRequest("isolation is worktree or empty")
	}
}

func (d *Daemon) beginWork(ctx context.Context, request dashboardActionRequest, cwd, alias string) (worktree.Plan, error) {
	holder, err := d.worktreeHolder(cwd)
	if err != nil {
		return worktree.Plan{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	plan, err := worktree.Begin(ctx, d.Registry, worktree.Spec{Cwd: cwd, Branch: strings.TrimSpace(request.Worktree), Base: strings.TrimSpace(request.Base), Name: alias, Holder: holder})
	var refused worktree.RefusedError
	if errors.As(err, &refused) {
		return plan, invalidRequest(fmt.Sprintf("worktree: %s", refused.Err))
	}
	if err != nil {
		return plan, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	return plan, nil
}

// worktreeHolder is the added worktree project holding cwd, whose own
// worktrees folder takes the worktree so the session stays in that project.
func (d *Daemon) worktreeHolder(cwd string) (string, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	if holder := projectresolve.WorktreeProjectHolding(records, cwd); holder != nil {
		return holder.WorkspaceRoot, nil
	}
	return "", nil
}

func (d *Daemon) abandonWork(plan worktree.Plan) {
	ctx, cancel := context.WithTimeout(context.Background(), surfaceOperationTimeout)
	defer cancel()
	if err := worktree.Abandon(ctx, d.Registry, plan); err != nil {
		d.log.Printf("take back worktree %s: %s", plan.WorktreePath, err)
	}
}

// worktreeCeiling moves a ceiling's roots inside the checkout to their places
// in the worktree, so the child works in its worktree and nowhere wider.
func worktreeCeiling(raw json.RawMessage, plan worktree.Plan) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	ceiling, err := parseCeiling(raw)
	if err != nil {
		return nil, err
	}
	for i, root := range ceiling.Roots {
		if moved, ok := plan.Translate(root); ok {
			ceiling.Roots[i] = moved
		}
	}
	return ceiling.encode(), nil
}

func workResult(plan worktree.Plan) map[string]any {
	result := map[string]any{"path": plan.WorktreePath, "branch": plan.Branch, "base": plan.BaseRef}
	if plan.Behind > 0 {
		result["behind"] = plan.Behind
	}
	return result
}
