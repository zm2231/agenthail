package daemon

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/prwatch"
	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/worktree"
)

// handMadeSweepEvery is how often the worktrees folders of known projects are
// searched for worktrees no record owns.
const (
	handMadeSweepEvery   = 10 * time.Minute
	handMadeSweepTimeout = 5 * time.Minute
)

type handMadeWorktree struct {
	root   string
	listed worktree.Listed
	branch github.Branch
}

// sweepHandMadeWorktrees removes the worktrees in each project's worktrees
// folder that Agenthail did not record, once a pull request from the branch
// has merged and no session that is not archived has its folder inside. The
// same check as Remove decides: nothing uncommitted, no ignored files outside
// build and package folders, and every commit merged, on a remote, or in the
// head of a merged pull request.
func (d *Daemon) sweepHandMadeWorktrees(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, handMadeSweepTimeout)
	defer cancel()
	var found []handMadeWorktree
	for _, root := range d.projectCheckouts(ctx) {
		listed, err := worktree.List(ctx, root)
		if err != nil {
			d.log.Printf("list worktrees in %s: %s", root, err)
			continue
		}
		for _, entry := range listed {
			_, recorded, err := d.Registry.WorkAt(entry.Path)
			if err != nil {
				d.log.Printf("read the record of worktree %s: %s", entry.Path, err)
				continue
			}
			if recorded {
				continue
			}
			if d.worktreeUse(entry.Path, "") != worktreeFree {
				continue
			}
			repo, ok := prwatch.BranchRepo(ctx, root, entry.Branch)
			if !ok {
				continue
			}
			found = append(found, handMadeWorktree{root: root, listed: entry, branch: github.Branch{Repo: repo, Head: entry.Branch}})
		}
	}
	if len(found) == 0 {
		return
	}
	branches := make([]github.Branch, 0, len(found))
	for _, item := range found {
		if !slices.Contains(branches, item.branch) {
			branches = append(branches, item.branch)
		}
	}
	prs, err := d.github.BranchPullRequests(ctx, branches)
	if err != nil {
		d.log.Printf("read pull requests of hand-made worktrees: %s", err)
		return
	}
	for _, item := range found {
		heads, urls := mergedHeads(prs[item.branch], item.branch.Head, worktree.DefaultBranch(ctx, item.root))
		if len(heads) == 0 {
			continue
		}
		d.removeHandMadeWorktree(ctx, item, heads, urls)
	}
}

func (d *Daemon) removeHandMadeWorktree(ctx context.Context, item handMadeWorktree, heads, urls []string) {
	d.workClean.Lock()
	defer d.workClean.Unlock()
	if d.worktreeUse(item.listed.Path, "") != worktreeFree {
		return
	}
	record := worktree.Record{RepoRoot: item.root, WorktreePath: item.listed.Path, Branch: item.listed.Branch, BaseRef: worktree.DefaultBase(ctx, item.root)}
	outcome, err := worktree.Clean(ctx, record, worktree.Options{MergedHeads: heads})
	switch {
	case err != nil:
		d.log.Printf("remove hand-made worktree %s: %s", item.listed.Path, err)
	case outcome.Removed:
		d.log.Printf("removed hand-made worktree %s: %s merged", item.listed.Path, strings.Join(urls, ", "))
	}
}

// projectCheckouts are the main checkouts of the projects this host knows,
// each once.
func (d *Daemon) projectCheckouts(ctx context.Context) []string {
	projects, err := d.Registry.ListProjects()
	if err != nil {
		d.log.Printf("list projects for worktrees: %s", err)
		return nil
	}
	var roots []string
	for _, project := range projects {
		if !project.ArchivedAt.IsZero() || project.WorkspaceRoot == "" {
			continue
		}
		root, err := worktree.MainCheckout(ctx, project.WorkspaceRoot)
		if err == nil && !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	return roots
}
