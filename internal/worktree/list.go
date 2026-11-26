package worktree

import (
	"context"
	"path/filepath"
	"strings"
)

// Listed is a worktree git knows in a main checkout's worktrees folder, with
// the branch it has checked out.
type Listed struct {
	Path   string
	Branch string
}

// List is the worktrees in repoRoot's worktrees folder that have a branch
// checked out. A detached worktree names no branch to find a pull request by.
func List(ctx context.Context, repoRoot string) ([]Listed, error) {
	out, err := git(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(repoRoot, Dir)
	var listed []Listed
	for block := range strings.SplitSeq(out, "\n\n") {
		var entry Listed
		for line := range strings.SplitSeq(block, "\n") {
			if path, ok := strings.CutPrefix(line, "worktree "); ok {
				entry.Path = path
			}
			if branch, ok := strings.CutPrefix(line, "branch refs/heads/"); ok {
				entry.Branch = branch
			}
		}
		if entry.Branch != "" && filepath.Dir(entry.Path) == dir {
			listed = append(listed, entry)
		}
	}
	return listed, nil
}

// MainCheckout is the main checkout of the repository dir is in.
func MainCheckout(ctx context.Context, dir string) (string, error) {
	toplevel, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotRepository
	}
	return mainWorktree(ctx, dir, toplevel)
}

// DefaultBase is the ref branches in repoRoot merge into: its origin's
// default branch, else the main checkout's HEAD.
func DefaultBase(ctx context.Context, repoRoot string) string {
	if ref, err := git(ctx, repoRoot, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return ref
	}
	if branch := mainBranch(ctx, repoRoot); branch != "" {
		return branch
	}
	return "HEAD"
}

// DefaultBranch names the branch pull requests in repoRoot merge into: its
// origin's default branch, else the branch the main checkout has out. Empty
// when neither is known.
func DefaultBranch(ctx context.Context, repoRoot string) string {
	if ref, err := git(ctx, repoRoot, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return strings.TrimPrefix(ref, "origin/")
	}
	return mainBranch(ctx, repoRoot)
}

// mainBranch is the branch the main checkout has out, which repoRoot, a
// linked worktree on a branch of its own, does not share.
func mainBranch(ctx context.Context, repoRoot string) string {
	main, err := MainCheckout(ctx, repoRoot)
	if err != nil {
		return ""
	}
	branch, _ := git(ctx, main, "symbolic-ref", "--quiet", "--short", "HEAD")
	return branch
}
