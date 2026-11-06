package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/worktree/worktreetest"
)

var (
	run        = worktreetest.Git
	repository = worktreetest.PushedCheckout
)

func made(t *testing.T, spec Spec) Plan {
	t.Helper()
	plan, err := NewPlan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if plan, err = Add(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func record(plan Plan) Record {
	return Record{RepoRoot: plan.RepoRoot, WorktreePath: plan.WorktreePath, Branch: plan.Branch, BaseRef: plan.BaseRef, BaseCommit: plan.BaseCommit}
}

func branchExists(t *testing.T, checkout, branch string) bool {
	t.Helper()
	return run(t, checkout, "branch", "--list", branch) != ""
}

func TestATaskWorktreeIsMadeBesideTheCheckoutAndRemovedOnceItsWorkIsPushed(t *testing.T) {
	checkout := repository(t)
	plan := made(t, Spec{Cwd: filepath.Join(checkout, "app"), Name: "@builder"})
	resolved, _ := filepath.EvalSymlinks(checkout)
	if plan.Branch != "ah/builder" || plan.WorktreePath != filepath.Join(resolved, ".worktrees", "ah-builder") || plan.Cwd != filepath.Join(plan.WorktreePath, "app") || plan.BaseRef != "main" {
		t.Fatalf("plan %+v", plan)
	}
	if status := run(t, checkout, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("the main checkout shows the worktree: %q", status)
	}
	if gitignore, err := os.ReadFile(filepath.Join(checkout, ".gitignore")); err == nil {
		t.Fatalf("the repository's .gitignore was written: %q", gitignore)
	}

	if err := os.WriteFile(filepath.Join(plan.Cwd, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, err := Clean(context.Background(), record(plan)); err != nil || outcome.KeptReason != KeptDirty {
		t.Fatalf("an uncommitted file: %+v err=%v", outcome, err)
	}
	run(t, plan.Cwd, "add", ".")
	run(t, plan.Cwd, "commit", "--quiet", "-m", "feature")
	if outcome, err := Clean(context.Background(), record(plan)); err != nil || outcome.KeptReason != KeptUnpushed {
		t.Fatalf("a commit on no remote: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(plan.WorktreePath); err != nil || !branchExists(t, checkout, plan.Branch) {
		t.Fatalf("kept work was removed: %v", err)
	}

	run(t, plan.Cwd, "push", "--quiet", "origin", plan.Branch)
	if outcome, err := Clean(context.Background(), record(plan)); err != nil || !outcome.Removed {
		t.Fatalf("pushed work: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(plan.WorktreePath); !os.IsNotExist(err) || branchExists(t, checkout, plan.Branch) {
		t.Fatalf("the worktree or branch is still there: %v", err)
	}
}

func TestAWorktreeWhoseWorkWasMergedOrNeverStartedIsRemoved(t *testing.T) {
	checkout := repository(t)
	untouched := made(t, Spec{Cwd: checkout, Branch: "ah/untouched"})
	if outcome, err := Clean(context.Background(), record(untouched)); err != nil || !outcome.Removed || branchExists(t, checkout, "ah/untouched") {
		t.Fatalf("an untouched worktree: %+v err=%v", outcome, err)
	}

	merged := made(t, Spec{Cwd: checkout, Branch: "ah/merged"})
	if err := os.WriteFile(filepath.Join(merged.Cwd, "fix.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, merged.Cwd, "add", ".")
	run(t, merged.Cwd, "commit", "--quiet", "-m", "fix")
	run(t, checkout, "merge", "--quiet", "--ff-only", "ah/merged")
	if outcome, err := Clean(context.Background(), record(merged)); err != nil || !outcome.Removed || branchExists(t, checkout, "ah/merged") {
		t.Fatalf("work merged into the base but never pushed: %+v err=%v", outcome, err)
	}
}

func TestAWorktreeIsRefusedWhereItWouldOverwriteOrHasNoRepository(t *testing.T) {
	checkout := repository(t)
	made(t, Spec{Cwd: checkout, Branch: "ah/taken"})
	if _, err := NewPlan(context.Background(), Spec{Cwd: checkout, Branch: "ah/taken"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a branch that exists: %v", err)
	}
	if _, err := NewPlan(context.Background(), Spec{Cwd: checkout, Branch: "bad..name"}); err == nil {
		t.Fatal("an invalid branch name was planned")
	}
	if _, err := NewPlan(context.Background(), Spec{Cwd: t.TempDir()}); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("a folder outside any repository: %v", err)
	}
	if plan, err := NewPlan(context.Background(), Spec{Cwd: checkout, Base: "nope"}); err == nil {
		t.Fatalf("an unknown base was planned: %+v", plan)
	}
}

func TestAWorktreeForASessionThatNeverStartedIsTakenBack(t *testing.T) {
	checkout := repository(t)
	plan := made(t, Spec{Cwd: checkout, Branch: "ah/never"})
	if err := Discard(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.WorktreePath); !os.IsNotExist(err) || branchExists(t, checkout, "ah/never") {
		t.Fatalf("the worktree or branch is still there: %v", err)
	}
}

func TestABaseBehindItsUpstreamIsReported(t *testing.T) {
	checkout := repository(t)
	other := filepath.Join(t.TempDir(), "other")
	run(t, checkout, "clone", "--quiet", run(t, checkout, "remote", "get-url", "origin"), other)
	if err := os.WriteFile(filepath.Join(other, "later.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, other, "add", ".")
	run(t, other, "commit", "--quiet", "-m", "later")
	run(t, other, "push", "--quiet", "origin", "HEAD:main")
	run(t, checkout, "fetch", "--quiet")
	plan, err := NewPlan(context.Background(), Spec{Cwd: checkout, Branch: "ah/behind"})
	if err != nil || plan.Behind != 1 {
		t.Fatalf("plan %+v err=%v", plan, err)
	}
}
