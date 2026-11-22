package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func cleanOf(r Record) (Outcome, error) {
	return Clean(context.Background(), r, Options{})
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
	if outcome, err := cleanOf(record(plan)); err != nil || outcome.KeptReason != KeptDirty {
		t.Fatalf("an uncommitted file: %+v err=%v", outcome, err)
	}
	run(t, plan.Cwd, "add", ".")
	run(t, plan.Cwd, "commit", "--quiet", "-m", "feature")
	if outcome, err := cleanOf(record(plan)); err != nil || outcome.KeptReason != KeptUnpushed {
		t.Fatalf("a commit on no remote: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(plan.WorktreePath); err != nil || !branchExists(t, checkout, plan.Branch) {
		t.Fatalf("kept work was removed: %v", err)
	}

	run(t, plan.Cwd, "push", "--quiet", "origin", plan.Branch)
	if outcome, err := cleanOf(record(plan)); err != nil || !outcome.Removed {
		t.Fatalf("pushed work: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(plan.WorktreePath); !os.IsNotExist(err) || branchExists(t, checkout, plan.Branch) {
		t.Fatalf("the worktree or branch is still there: %v", err)
	}
}

func TestAWorktreeWhoseWorkWasMergedOrNeverStartedIsRemoved(t *testing.T) {
	checkout := repository(t)
	untouched := made(t, Spec{Cwd: checkout, Branch: "ah/untouched"})
	if outcome, err := cleanOf(record(untouched)); err != nil || !outcome.Removed || branchExists(t, checkout, "ah/untouched") {
		t.Fatalf("an untouched worktree: %+v err=%v", outcome, err)
	}

	merged := made(t, Spec{Cwd: checkout, Branch: "ah/merged"})
	if err := os.WriteFile(filepath.Join(merged.Cwd, "fix.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, merged.Cwd, "add", ".")
	run(t, merged.Cwd, "commit", "--quiet", "-m", "fix")
	run(t, checkout, "merge", "--quiet", "--ff-only", "ah/merged")
	if outcome, err := cleanOf(record(merged)); err != nil || !outcome.Removed || branchExists(t, checkout, "ah/merged") {
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

func TestIgnoredFilesAndCommitsOffTheBranchKeepTheWorktree(t *testing.T) {
	checkout := repository(t)
	ignored := made(t, Spec{Cwd: checkout, Branch: "ah/ignored"})
	if err := os.WriteFile(filepath.Join(checkout, ".git", "info", "exclude"), []byte("/.worktrees/\n*.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored.WorktreePath, "local.env"), []byte("TOKEN=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, err := cleanOf(record(ignored)); err != nil || outcome.KeptReason != KeptIgnored || len(outcome.IgnoredPaths) != 1 || outcome.IgnoredPaths[0] != "local.env" {
		t.Fatalf("an ignored file: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(ignored.WorktreePath, "local.env")); err != nil {
		t.Fatalf("the ignored file was removed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ignored.WorktreePath, "second.env"), []byte("TOKEN=y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outcome, err := Clean(context.Background(), record(ignored), Options{RemoveIgnored: []string{"local.env"}}); err != nil || outcome.Removed || outcome.KeptReason != KeptIgnored || !slices.Equal(outcome.IgnoredPaths, []string{"local.env", "second.env"}) {
		t.Fatalf("an ignored file the person was not shown must keep the worktree and be named: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(ignored.WorktreePath, "second.env")); err != nil {
		t.Fatalf("an ignored file the person did not confirm was removed: %v", err)
	}
	if outcome, err := Clean(context.Background(), record(ignored), Options{RemoveIgnored: []string{"second.env", "local.env", "second.env"}}); err != nil || !outcome.Removed {
		t.Fatalf("the person removing exactly the ignored files they were shown: %+v err=%v", outcome, err)
	}

	crowded := made(t, Spec{Cwd: checkout, Branch: "ah/crowded"})
	var all []string
	for i := range KeptPathLimit + 1 {
		name := fmt.Sprintf("k%02d.env", i)
		all = append(all, name)
		if err := os.WriteFile(filepath.Join(crowded.WorktreePath, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if outcome, err := Clean(context.Background(), record(crowded), Options{RemoveIgnored: all}); err != nil || outcome.Removed || outcome.KeptReason != KeptIgnoredMany || len(outcome.IgnoredPaths) != KeptPathLimit {
		t.Fatalf("more ignored files than can be named are never removed from a list: %+v err=%v", outcome, err)
	}

	built := made(t, Spec{Cwd: checkout, Branch: "ah/built"})
	if err := os.WriteFile(filepath.Join(checkout, ".git", "info", "exclude"), []byte("/.worktrees/\nnode_modules/\ndist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"node_modules/left-pad/index.js", "app/dist/bundle.js"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(built.WorktreePath, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(built.WorktreePath, path), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if outcome, err := cleanOf(record(built)); err != nil || !outcome.Removed {
		t.Fatalf("only build output: %+v err=%v", outcome, err)
	}

	detached := made(t, Spec{Cwd: checkout, Branch: "ah/detached"})
	run(t, detached.WorktreePath, "checkout", "--quiet", "--detach")
	if err := os.WriteFile(filepath.Join(detached.WorktreePath, "loose.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, detached.WorktreePath, "add", ".")
	run(t, detached.WorktreePath, "commit", "--quiet", "-m", "loose")
	run(t, checkout, "branch", "--quiet", "-D", "ah/detached")
	if outcome, err := cleanOf(record(detached)); err != nil || outcome.KeptReason != KeptUnpushed {
		t.Fatalf("a commit only the worktree's HEAD reaches: %+v err=%v", outcome, err)
	}
	if _, err := os.Stat(filepath.Join(detached.WorktreePath, "loose.go")); err != nil {
		t.Fatalf("the detached work was removed: %v", err)
	}
}

func TestASubmoduleGetsWorktreesBesideItsOwnCheckout(t *testing.T) {
	library := repository(t)
	parent := filepath.Join(t.TempDir(), "parent")
	run(t, filepath.Dir(parent), "init", "--quiet", "--initial-branch=main", parent)
	run(t, parent, "-c", "protocol.file.allow=always", "submodule", "--quiet", "add", library, "lib")
	run(t, parent, "commit", "--quiet", "-m", "add lib")
	sub := filepath.Join(parent, "lib")
	plan := made(t, Spec{Cwd: filepath.Join(sub, "app"), Branch: "ah/sub"})
	resolved, _ := filepath.EvalSymlinks(sub)
	if plan.RepoRoot != resolved || plan.Cwd != filepath.Join(resolved, Dir, "ah-sub", "app") {
		t.Fatalf("plan %+v", plan)
	}
	if status := run(t, sub, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("the submodule shows its worktrees: %q", status)
	}
	nested, err := NewPlan(context.Background(), Spec{Cwd: plan.Cwd, Branch: "ah/nested"})
	if err != nil || nested.RepoRoot != resolved || nested.Cwd != filepath.Join(resolved, Dir, "ah-nested", "app") {
		t.Fatalf("from inside a submodule's worktree: %+v err=%v", nested, err)
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

func TestASquashMergedBranchIsRemovedOnlyWhenItsTipIsInTheMergedHead(t *testing.T) {
	checkout := repository(t)
	commit := func(dir, name string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "add", ".")
		run(t, dir, "commit", "--quiet", "-m", name)
		return run(t, dir, "rev-parse", "HEAD")
	}
	squashed := func(branch string) (Plan, string) {
		t.Helper()
		plan := made(t, Spec{Cwd: checkout, Branch: branch})
		commit(plan.Cwd, Slug(branch)+"-one.go")
		head := commit(plan.Cwd, Slug(branch)+"-two.go")
		run(t, plan.Cwd, "push", "--quiet", "origin", "HEAD:"+branch)
		run(t, checkout, "merge", "--quiet", "--squash", branch)
		run(t, checkout, "commit", "--quiet", "-m", "squash "+branch)
		run(t, checkout, "push", "--quiet", "origin", "HEAD:main")
		run(t, checkout, "push", "--quiet", "origin", "--delete", branch)
		run(t, checkout, "fetch", "--quiet", "--prune")
		return plan, head
	}

	done, head := squashed("ah/done")
	if outcome, err := cleanOf(record(done)); err != nil || outcome.Removed || outcome.KeptReason != KeptUnpushed {
		t.Fatalf("without the merged head a squash-merged branch reads as unpushed: %+v err=%v", outcome, err)
	}
	if outcome, err := Clean(context.Background(), record(done), Options{MergedHeads: []string{head}}); err != nil || !outcome.Removed || branchExists(t, checkout, "ah/done") {
		t.Fatalf("a branch whose tip is its merged pull request's head: %+v err=%v", outcome, err)
	}

	more, head := squashed("ah/more")
	commit(more.Cwd, "after.go")
	if outcome, err := Clean(context.Background(), record(more), Options{MergedHeads: []string{head}}); err != nil || outcome.Removed || outcome.KeptReason != KeptUnpushed {
		t.Fatalf("a commit after the merged head keeps the worktree: %+v err=%v", outcome, err)
	}
}

func TestListNamesTheBranchWorktreesInTheWorktreesFolder(t *testing.T) {
	checkout := repository(t)
	made(t, Spec{Cwd: checkout, Branch: "ah/listed"})
	detached := filepath.Join(checkout, Dir, "detached")
	run(t, checkout, "worktree", "add", "--quiet", "--detach", detached)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	run(t, checkout, "worktree", "add", "--quiet", "-b", "ah/elsewhere", elsewhere)
	listed, err := List(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Listed{{Path: filepath.Join(checkout, Dir, "ah-listed"), Branch: "ah/listed"}}; !slices.Equal(listed, want) {
		t.Fatalf("listed %+v, want %+v", listed, want)
	}
	if root, err := MainCheckout(context.Background(), filepath.Join(checkout, Dir, "ah-listed")); err != nil || root != checkout {
		t.Fatalf("main checkout %q err=%v", root, err)
	}
}
