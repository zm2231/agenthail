package worktree

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

type RefusedError struct{ Err error }

func (e RefusedError) Error() string { return e.Err.Error() }
func (e RefusedError) Unwrap() error { return e.Err }

// Begin records the worktree before making it, so a crash between the two
// leaves a row to reconcile rather than a folder nobody tracks.
func Begin(ctx context.Context, reg *registry.Registry, spec Spec) (Plan, error) {
	release, err := lockRepository(ctx, spec.Cwd)
	if errors.Is(err, ErrNotRepository) {
		return Plan{}, RefusedError{fmt.Errorf("a worktree needs a git repository, and %s is not in one", spec.Cwd)}
	}
	if err != nil {
		return Plan{}, fmt.Errorf("lock the repository's worktrees: %w", err)
	}
	defer release()
	plan, err := NewPlan(ctx, spec)
	if errors.Is(err, ErrNotRepository) {
		return plan, RefusedError{fmt.Errorf("a worktree needs a git repository, and %s is not in one", spec.Cwd)}
	}
	if err != nil {
		return plan, RefusedError{err}
	}
	if _, found, err := reg.WorkAt(plan.WorktreePath); err != nil {
		return plan, fmt.Errorf("record worktree: %w", err)
	} else if found {
		return plan, RefusedError{fmt.Errorf("a worktree at %s is already recorded", plan.WorktreePath)}
	}
	if err := reg.BeginWork(registry.WorkRecord{WorktreePath: plan.WorktreePath, RepoRoot: plan.RepoRoot, Branch: plan.Branch, BaseRef: plan.BaseRef, BaseCommit: plan.BaseCommit}, time.Now()); err != nil {
		return plan, fmt.Errorf("record worktree: %w", err)
	}
	made, err := Add(ctx, plan)
	if err != nil {
		return plan, errors.Join(fmt.Errorf("make worktree: %w", err), abandonLocked(context.WithoutCancel(ctx), reg, plan))
	}
	return made, nil
}

// Abandon keeps the record when the worktree could not be discarded, so the
// reconcile retries it.
func Abandon(ctx context.Context, reg *registry.Registry, plan Plan) error {
	release, err := lockRepository(ctx, plan.RepoRoot)
	if err != nil {
		return err
	}
	defer release()
	return abandonLocked(ctx, reg, plan)
}

func abandonLocked(ctx context.Context, reg *registry.Registry, plan Plan) error {
	if err := Discard(ctx, plan); err != nil {
		return err
	}
	return reg.DropWork(plan.WorktreePath)
}
