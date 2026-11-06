package worktree

import (
	"context"
	"os"
)

const (
	KeptDirty    = "dirty"
	KeptUnpushed = "unpushed"
)

type Record struct {
	RepoRoot     string
	WorktreePath string
	Branch       string
	BaseRef      string
	BaseCommit   string
}

type Outcome struct {
	Removed    bool
	KeptReason string
}

// Clean removes a worktree and its branch when nothing in them is lost: no
// tracked or untracked changes, and every commit either merged into the base
// or on a remote. Otherwise it keeps both and says why. The worktree is
// removed without force, so git refuses anything this check missed.
func Clean(ctx context.Context, record Record) (Outcome, error) {
	if _, err := os.Stat(record.WorktreePath); err == nil {
		if status, err := git(ctx, record.WorktreePath, "status", "--porcelain", "--untracked-files=all"); err != nil {
			return Outcome{}, err
		} else if status != "" {
			return Outcome{KeptReason: KeptDirty}, nil
		}
	} else if !os.IsNotExist(err) {
		return Outcome{}, err
	} else if _, err := git(ctx, record.RepoRoot, "worktree", "prune"); err != nil {
		return Outcome{}, err
	}
	tip, err := git(ctx, record.RepoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+record.Branch)
	if err != nil {
		tip = ""
	}
	if tip != "" && tip != record.BaseCommit {
		safe, err := gitOK(ctx, record.RepoRoot, "merge-base", "--is-ancestor", tip, record.BaseRef)
		if err != nil {
			safe = false
		}
		if !safe {
			remote, err := git(ctx, record.RepoRoot, "branch", "--remotes", "--contains", tip)
			if err != nil {
				return Outcome{}, err
			}
			safe = remote != ""
		}
		if !safe {
			return Outcome{KeptReason: KeptUnpushed}, nil
		}
	}
	if _, err := os.Stat(record.WorktreePath); err == nil {
		if _, err := git(ctx, record.RepoRoot, "worktree", "remove", record.WorktreePath); err != nil {
			return Outcome{}, err
		}
	}
	if tip != "" {
		if _, err := git(ctx, record.RepoRoot, "branch", "-D", record.Branch); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{Removed: true}, nil
}
