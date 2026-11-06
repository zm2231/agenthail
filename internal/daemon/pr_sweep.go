package daemon

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	prSweepEvery   = 2 * time.Minute
	prSweepTimeout = time.Minute
)

type branchWork struct {
	record registry.WorkRecord
	branch github.Branch
}

func (d *Daemon) runPRSweeps(ctx context.Context) {
	ticker := time.NewTicker(prSweepEvery)
	defer ticker.Stop()
	lastFailure := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		failure := ""
		if err := d.linkBranchPRs(ctx); err != nil {
			failure = err.Error()
		}
		if failure != lastFailure && failure != "" {
			d.log.Printf("pull requests on worktree branches: %s", failure)
		}
		lastFailure = failure
	}
}

func (d *Daemon) linkBranchPRs(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, prSweepTimeout)
	defer cancel()
	records, err := d.Registry.SessionWork()
	if err != nil {
		return err
	}
	var work []branchWork
	for _, record := range records {
		if record.Cleanup == registry.WorkCleanupRemoved {
			continue
		}
		repo, ok := branchRepo(ctx, record)
		if !ok {
			continue
		}
		known, err := d.Registry.SessionHasPRIn(record.SessionID, repo.String())
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
	found, err := d.github.BranchPullRequests(ctx, branches)
	if err != nil {
		return err
	}
	linked := false
	for _, item := range work {
		for _, pr := range found[item.branch] {
			if pr.CreatedAt.Before(item.record.CreatedAt) {
				continue
			}
			added, err := d.Registry.LinkPR(registry.SessionPR{SessionID: item.record.SessionID, Repo: pr.Repo.String(), Number: pr.Number, URL: pr.URL, Source: registry.PRSourceBranch, Title: pr.Title, State: pr.State, HeadSHA: pr.HeadSHA}, false, time.Now())
			if err != nil {
				return err
			}
			linked = linked || added
		}
	}
	if linked {
		d.requestCatalogDiscovery()
	}
	return nil
}

func branchRepo(ctx context.Context, record registry.WorkRecord) (github.Repo, bool) {
	git := func(args ...string) string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", record.RepoRoot}, args...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	remote := git("config", "branch."+record.Branch+".remote")
	if remote == "" || remote == "." {
		remote = "origin"
	}
	return github.ParseRepo(git("remote", "get-url", remote))
}
