package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/prwatch"
	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/worktree"
)

const (
	workCleanEvery = time.Minute
	// A create runs outside the daemon too (the CLI, with any --timeout), so
	// only work this old is taken for a crashed create.
	workCreatingStale = time.Hour
	workCleanTimeout  = time.Minute
)

func (d *Daemon) runWorkCleans(ctx context.Context) {
	ticker := time.NewTicker(workCleanEvery)
	defer ticker.Stop()
	var handMadeSwept time.Time
	for {
		now := time.Now()
		d.sweepWork(ctx, now)
		if now.Sub(handMadeSwept) >= handMadeSweepEvery {
			d.sweepHandMadeWorktrees(ctx)
			handMadeSwept = now
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.workWake:
		}
	}
}

func (d *Daemon) wakeWorkClean() {
	select {
	case d.workWake <- struct{}{}:
	default:
	}
}

func (d *Daemon) changeWork(write func(*registry.Registry) (bool, error)) (bool, error) {
	changed, err := write(d.Registry)
	if err == nil && changed {
		d.requestCatalogDiscovery()
	}
	return changed, err
}

func (d *Daemon) settleWork(sessionID, reason string) {
	settled, err := d.changeWork(func(r *registry.Registry) (bool, error) { return r.SettleWork(sessionID, reason, time.Now()) })
	if err != nil {
		d.log.Printf("settle worktree of %s: %s", d.resolveDisplay(sessionID), err)
		return
	}
	if settled {
		d.wakeWorkClean()
	}
}

func (d *Daemon) sweepWork(ctx context.Context, now time.Time) {
	d.reconcileCreatingWork(now)
	records, err := d.Registry.WorkToClean()
	if err != nil {
		d.log.Printf("list worktrees to clean: %s", err)
		return
	}
	for _, record := range records {
		if _, err := d.cleanWork(ctx, record.WorktreePath, worktree.Options{}, true); err != nil {
			d.log.Printf("clean worktree %s: %s", record.WorktreePath, err)
		}
	}
}

// reconcileCreatingWork settles work a create recorded but never finished:
// a session that started in it takes it, and otherwise it is cleaned.
func (d *Daemon) reconcileCreatingWork(now time.Time) {
	stale, err := d.Registry.StaleCreatingWork(now.Add(-workCreatingStale))
	if err != nil || len(stale) == 0 {
		if err != nil {
			d.log.Printf("list unfinished worktrees: %s", err)
		}
		return
	}
	sessions, err := d.Registry.ListSessions(0)
	if err != nil {
		d.log.Printf("list sessions for unfinished worktrees: %s", err)
		return
	}
	for _, record := range stale {
		owner := ""
		for _, session := range sessions {
			if underRoot(session.Cwd, []string{record.WorktreePath}) {
				owner = session.ID
				break
			}
		}
		if owner != "" {
			if _, err := d.changeWork(func(r *registry.Registry) (bool, error) { return r.ActivateWork(record.WorktreePath, owner) }); err != nil {
				d.log.Printf("give worktree %s to %s: %s", record.WorktreePath, d.resolveDisplay(owner), err)
			}
			continue
		}
		if _, err := d.changeWork(func(r *registry.Registry) (bool, error) { return r.SettleOrphanWork(record.WorktreePath, now) }); err != nil {
			d.log.Printf("settle unfinished worktree %s: %s", record.WorktreePath, err)
		}
	}
}

// cleanWork removes settled work when nothing in it would be lost, and keeps
// it with the reason otherwise. Work a session is mid-turn in waits, and
// automatic cleanup also waits while a session that is not archived has it
// open; the person's own request does not.
func (d *Daemon) cleanWork(ctx context.Context, worktreePath string, options worktree.Options, automatic bool) (registry.WorkRecord, error) {
	d.workClean.Lock()
	defer d.workClean.Unlock()
	record, found, err := d.Registry.WorkAt(worktreePath)
	if err != nil || !found || record.State != registry.WorkSettled || record.Cleanup == registry.WorkCleanupRemoved {
		return record, err
	}
	switch d.worktreeUse(record.WorktreePath, record.SessionID) {
	case worktreeWorking:
		return d.setWorkCleanup(record, registry.WorkCleanupKept, registry.WorkKeptRunning, nil)
	case worktreeOpen:
		if automatic {
			return d.setWorkCleanup(record, registry.WorkCleanupKept, registry.WorkKeptOpen, nil)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, workCleanTimeout)
	defer cancel()
	options.MergedHeads = append(options.MergedHeads, d.branchMergedHeads(ctx, record.RepoRoot, record.Branch)...)
	outcome, err := worktree.Clean(ctx, worktree.Record{RepoRoot: record.RepoRoot, WorktreePath: record.WorktreePath, Branch: record.Branch, BaseRef: record.BaseRef, BaseCommit: record.BaseCommit}, options)
	if err != nil {
		return d.setWorkCleanup(record, registry.WorkCleanupKept, fmt.Sprintf("failed: %s", err), nil)
	}
	if !outcome.Removed {
		return d.setWorkCleanup(record, registry.WorkCleanupKept, outcome.KeptReason, outcome.IgnoredPaths)
	}
	if record.SessionID == "" {
		_, err := d.changeWork(func(r *registry.Registry) (bool, error) { return true, r.DropWork(record.WorktreePath) })
		return record, err
	}
	return d.setWorkCleanup(record, registry.WorkCleanupRemoved, "", nil)
}

func (d *Daemon) setWorkCleanup(record registry.WorkRecord, cleanup, reason string, keptPaths []string) (registry.WorkRecord, error) {
	if record.Cleanup == cleanup && record.CleanupReason == reason && slices.Equal(record.KeptPaths, keptPaths) {
		return record, nil
	}
	record.Cleanup, record.CleanupReason, record.KeptPaths = cleanup, reason, keptPaths
	_, err := d.changeWork(func(r *registry.Registry) (bool, error) {
		return true, r.SetWorkCleanup(record.WorktreePath, cleanup, reason, keptPaths)
	})
	return record, err
}

type worktreeUsage int

const (
	worktreeFree worktreeUsage = iota
	// worktreeOpen: a session that is not archived owns it or has its folder
	// inside it. Sessions sit idle for days and then resume there.
	worktreeOpen
	// worktreeWorking: such a session is mid-turn or running background work.
	worktreeWorking
)

// worktreeUse is how sessions use the worktree at path: its owner, if any,
// and every session whose folder is inside it. A read that fails counts as
// working, the safer answer before removing files.
func (d *Daemon) worktreeUse(path, owner string) worktreeUsage {
	sessions, err := d.Registry.ListSessions(0)
	if err != nil {
		return worktreeWorking
	}
	use := worktreeFree
	for _, listed := range sessions {
		if (owner == "" || listed.ID != owner) && !underRoot(listed.Cwd, []string{path}) {
			continue
		}
		// The listing shows a session missing from the catalog as offline;
		// its recorded status is the safer read before removing files.
		session, err := d.Registry.Session(listed.ID)
		if err != nil || session.Status == surface.StatusBusy || surface.BackgroundRunning(session.BackgroundTasks) {
			return worktreeWorking
		}
		archived, err := d.Registry.SessionArchived(listed.ID)
		if err != nil {
			return worktreeWorking
		}
		if !archived {
			use = worktreeOpen
		}
	}
	return use
}

// branchMergedHeads are the head commits of the pull requests merged from
// branch into the repository's default branch, read from GitHub. A failed
// read gives none, so the worktree is judged without them.
func (d *Daemon) branchMergedHeads(ctx context.Context, root, branch string) []string {
	repo, ok := prwatch.BranchRepo(ctx, root, branch)
	if !ok {
		return nil
	}
	target := github.Branch{Repo: repo, Head: branch}
	prs, err := d.github.BranchPullRequests(ctx, []github.Branch{target})
	if err != nil {
		d.log.Printf("read pull requests of branch %s: %s", branch, err)
		return nil
	}
	heads, _ := mergedHeads(prs[target], branch, worktree.DefaultBranch(ctx, root))
	return heads
}

// mergedHeads are the head commits, and links, of the pull requests among
// prs merged from branch into base. A pull request into any other branch
// never reached base, so its head proves nothing.
func mergedHeads(prs []github.PullRequest, branch, base string) (heads, urls []string) {
	if base == "" {
		return nil, nil
	}
	for _, pr := range prs {
		if pr.State == "merged" && pr.HeadRef == branch && pr.BaseRef == base && pr.HeadSHA != "" {
			heads, urls = append(heads, pr.HeadSHA), append(urls, pr.URL)
		}
	}
	return heads, urls
}

// doWorkClean is the person settling a session's worktree, or asking again
// for one that was kept.
func (d *Daemon) doWorkClean(ctx context.Context, p principal, sessionID string, options worktree.Options) (actionResult, error) {
	if p.Kind == principalSession {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "cleaning a worktree is the person's call")
	}
	record, found, err := d.Registry.WorkForSession(sessionID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if !found {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "this session has no worktree of its own")
	}
	if record.State == registry.WorkActive {
		if _, err := d.changeWork(func(r *registry.Registry) (bool, error) {
			return r.SettleWork(sessionID, registry.WorkSettledPerson, time.Now())
		}); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
	} else if record.Cleanup == registry.WorkCleanupKept {
		if _, err := d.setWorkCleanup(record, registry.WorkCleanupPending, "", nil); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
	}
	record, err = d.cleanWork(ctx, record.WorktreePath, options, false)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "sessionId": sessionID, "worktree": newWorkView(record)}), nil
}
