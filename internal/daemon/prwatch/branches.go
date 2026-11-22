package prwatch

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	SweepEvery     = 2 * time.Minute
	prSweepTimeout = time.Minute
)

type branchWork struct {
	record registry.WorkRecord
	branch github.Branch
}

// Run links worktree branches to their pull requests and watches the linked
// ones, every two minutes until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(SweepEvery)
	defer ticker.Stop()
	lastFailure := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		failure := ""
		if err := w.LinkBranches(ctx); err != nil {
			failure = "linking worktree branches: " + err.Error()
		}
		if err := w.Watch(ctx, time.Now()); err != nil {
			failure = strings.TrimPrefix(failure+"; watching: "+err.Error(), "; ")
		}
		if failure != lastFailure && failure != "" {
			w.log.Printf("pull requests: %s", failure)
		}
		lastFailure = failure
	}
}

// LinkBranches links each worktree branch to the pull requests opened from it
// since the worktree was made.
func (w *Watcher) LinkBranches(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, prSweepTimeout)
	defer cancel()
	records, err := w.store.SessionWork()
	if err != nil {
		return err
	}
	var work []branchWork
	for _, record := range records {
		if record.Cleanup == registry.WorkCleanupRemoved {
			continue
		}
		repo, ok := BranchRepo(ctx, record.RepoRoot, record.Branch)
		if !ok {
			continue
		}
		known, err := w.store.SessionHasPRIn(record.SessionID, repo.String())
		if err != nil {
			return err
		}
		if !known {
			work = append(work, branchWork{record: record, branch: github.Branch{Repo: repo, Head: record.Branch}})
		}
	}
	if len(work) == 0 {
		return nil
	}
	branches := make([]github.Branch, 0, len(work))
	for _, item := range work {
		branches = append(branches, item.branch)
	}
	found, err := w.host.GitHub().BranchPullRequests(ctx, branches)
	if err != nil {
		return err
	}
	for _, item := range work {
		for _, pr := range found[item.branch] {
			if pr.CreatedAt.Before(item.record.CreatedAt) {
				continue
			}
			if err := w.Link(registry.SessionPR{SessionID: item.record.SessionID, Repo: pr.Repo.String(), Number: pr.Number, URL: pr.URL, Source: registry.PRSourceBranch, Title: pr.Title, State: pr.State, HeadSHA: pr.HeadSHA}, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// BranchRepo is the GitHub repository a branch of the checkout at root pushes
// to: its configured remote, else origin.
func BranchRepo(ctx context.Context, root, branch string) (github.Repo, bool) {
	git := func(args ...string) string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	remote := git("config", "branch."+branch+".remote")
	if remote == "" || remote == "." {
		remote = "origin"
	}
	return github.ParseRepo(git("remote", "get-url", remote))
}
