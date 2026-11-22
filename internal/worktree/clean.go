package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	KeptDirty       = "dirty"
	KeptIgnored     = "ignored"
	KeptIgnoredMany = "ignored-many"
	KeptUnpushed    = "unpushed"
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

// Options.RemoveIgnored names the ignored files the person agreed to delete
// with the worktree. Clean removes it only while they are exactly the ignored
// files that would keep it. MergedHeads are the head commits of pull requests
// merged from the branch: a squash merge leaves them off the base, yet every
// commit they reach is merged.
type Options struct {
	RemoveIgnored []string
	MergedHeads   []string
}

// KeptPathLimit is how many ignored files a kept worktree names. Past it the
// worktree is kept as KeptIgnoredMany and only a person at a terminal removes it.
const KeptPathLimit = 50

// Clean removes a worktree and its branch when nothing in them is lost: no
// changed or untracked files, no ignored files outside regenerable folders,
// and every commit the branch or the worktree's own HEAD reaches either
// merged into the base or on a remote. Otherwise it keeps both and says why.
// The worktree is removed without force, so git refuses anything this check
// missed.
func Clean(ctx context.Context, record Record, options Options) (Outcome, error) {
	if filepath.Dir(record.WorktreePath) != filepath.Join(record.RepoRoot, Dir) {
		return Outcome{}, fmt.Errorf("%s is not in %s, where Agenthail makes worktrees", record.WorktreePath, filepath.Join(record.RepoRoot, Dir))
	}
	release, err := lockRepository(ctx, record.RepoRoot)
	if err != nil {
		return Outcome{}, err
	}
	defer release()
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
		safe, err := tipSafe(ctx, record, tip, options.MergedHeads)
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
	var paths []string
	count := 0
	for _, entry := range strings.Split(status, "\x00") {
		path, ignored := strings.CutPrefix(entry, "!! ")
		switch {
		case entry == "":
		case !ignored:
			return Outcome{KeptReason: KeptDirty}
		case Regenerable(path):
		default:
			count++
			if len(paths) < KeptPathLimit {
				paths = append(paths, path)
			}
		}
	}
	switch {
	case count == 0:
		return Outcome{}
	case count > KeptPathLimit:
		return Outcome{KeptReason: KeptIgnoredMany, IgnoredPaths: paths}
	case sameSet(paths, options.RemoveIgnored):
		return Outcome{}
	}
	return Outcome{KeptReason: KeptIgnored, IgnoredPaths: paths}
}

func sameSet(paths, named []string) bool {
	return slices.Equal(slices.Compact(slices.Sorted(slices.Values(paths))), slices.Compact(slices.Sorted(slices.Values(named))))
}

func tipSafe(ctx context.Context, record Record, tip string, mergedHeads []string) (bool, error) {
	if tip == record.BaseCommit || slices.Contains(mergedHeads, tip) {
		return true, nil
	}
	for _, head := range mergedHeads {
		if known, err := gitOK(ctx, record.RepoRoot, "cat-file", "-e", head+"^{commit}"); err != nil || !known {
			continue
		}
		if merged, err := gitOK(ctx, record.RepoRoot, "merge-base", "--is-ancestor", tip, head); err == nil && merged {
			return true, nil
		}
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
