package worktree

import (
	"context"
	"os"
	"strings"
)

const (
	KeptDirty    = "dirty"
	KeptIgnored  = "ignored"
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
	Removed      bool
	KeptReason   string
	IgnoredPaths []string
}

type Options struct {
	ForceIgnored bool
}

const keptPathLimit = 5

// Clean removes a worktree and its branch when nothing in them is lost: no
// changed or untracked files, no ignored files outside regenerable folders,
// and every commit the branch or the worktree's own HEAD reaches either
// merged into the base or on a remote. Otherwise it keeps both and says why.
// The worktree is removed without force, so git refuses anything this check
// missed.
func Clean(ctx context.Context, record Record, options Options) (Outcome, error) {
	var tips []string
	if _, err := os.Stat(record.WorktreePath); err == nil {
		status, err := git(ctx, record.WorktreePath, "status", "--porcelain", "-z", "--untracked-files=all", "--ignored")
		if err != nil {
			return Outcome{}, err
		}
		if kept := statusKept(status, options); kept.KeptReason != "" {
			return kept, nil
		}
		if head, err := git(ctx, record.WorktreePath, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
			tips = append(tips, head)
		}
	} else if !os.IsNotExist(err) {
		return Outcome{}, err
	} else if _, err := git(ctx, record.RepoRoot, "worktree", "prune"); err != nil {
		return Outcome{}, err
	}
	branch, err := git(ctx, record.RepoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+record.Branch)
	if err != nil {
		branch = ""
	} else {
		tips = append(tips, branch)
	}
	for _, tip := range tips {
		safe, err := tipSafe(ctx, record, tip)
		if err != nil {
			return Outcome{}, err
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
	if branch != "" {
		if _, err := git(ctx, record.RepoRoot, "branch", "-D", record.Branch); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{Removed: true}, nil
}

func statusKept(status string, options Options) Outcome {
	var kept Outcome
	for _, entry := range strings.Split(status, "\x00") {
		path, ignored := strings.CutPrefix(entry, "!! ")
		switch {
		case entry == "":
		case !ignored:
			return Outcome{KeptReason: KeptDirty}
		case options.ForceIgnored || Regenerable(path):
		default:
			kept.KeptReason = KeptIgnored
			if len(kept.IgnoredPaths) < keptPathLimit {
				kept.IgnoredPaths = append(kept.IgnoredPaths, path)
			}
		}
	}
	return kept
}

func tipSafe(ctx context.Context, record Record, tip string) (bool, error) {
	if tip == record.BaseCommit {
		return true, nil
	}
	if merged, err := gitOK(ctx, record.RepoRoot, "merge-base", "--is-ancestor", tip, record.BaseRef); err == nil && merged {
		return true, nil
	}
	remote, err := git(ctx, record.RepoRoot, "branch", "--remotes", "--contains", tip)
	if err != nil {
		return false, err
	}
	return remote != "", nil
}
