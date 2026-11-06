package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

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
	for {
		d.sweepWork(ctx, time.Now())
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

func (d *Daemon) settleWork(sessionID, reason string) {
	settled, err := d.Registry.SettleWork(sessionID, reason, time.Now())
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
		if _, err := d.cleanWork(ctx, record.WorktreePath, worktree.Options{}); err != nil {
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
			if _, err := d.Registry.ActivateWork(record.WorktreePath, owner); err != nil {
				d.log.Printf("give worktree %s to %s: %s", record.WorktreePath, d.resolveDisplay(owner), err)
			}
			continue
		}
		if _, err := d.Registry.SettleOrphanWork(record.WorktreePath, now); err != nil {
			d.log.Printf("settle unfinished worktree %s: %s", record.WorktreePath, err)
		}
	}
}

// cleanWork removes settled work when nothing in it would be lost, and keeps
// it with the reason otherwise. Work whose session is mid-turn waits.
func (d *Daemon) cleanWork(ctx context.Context, worktreePath string, options worktree.Options) (registry.WorkRecord, error) {
	d.workClean.Lock()
	defer d.workClean.Unlock()
	record, found, err := d.Registry.WorkAt(worktreePath)
	if err != nil || !found || record.State != registry.WorkSettled || record.Cleanup == registry.WorkCleanupRemoved {
		return record, err
	}
	if d.sessionWorking(record.SessionID) {
		return d.setWorkCleanup(record, registry.WorkCleanupKept, registry.WorkKeptRunning, nil)
	}
	ctx, cancel := context.WithTimeout(ctx, workCleanTimeout)
	defer cancel()
	outcome, err := worktree.Clean(ctx, worktree.Record{RepoRoot: record.RepoRoot, WorktreePath: record.WorktreePath, Branch: record.Branch, BaseRef: record.BaseRef, BaseCommit: record.BaseCommit}, options)
	if err != nil {
		return d.setWorkCleanup(record, registry.WorkCleanupKept, fmt.Sprintf("failed: %s", err), nil)
	}
	if !outcome.Removed {
		return d.setWorkCleanup(record, registry.WorkCleanupKept, outcome.KeptReason, outcome.IgnoredPaths)
	}
	if record.SessionID == "" {
		return record, d.Registry.DropWork(record.WorktreePath)
	}
	return d.setWorkCleanup(record, registry.WorkCleanupRemoved, "", nil)
}

func (d *Daemon) setWorkCleanup(record registry.WorkRecord, cleanup, reason string, keptPaths []string) (registry.WorkRecord, error) {
	if record.Cleanup == cleanup && record.CleanupReason == reason && slices.Equal(record.KeptPaths, keptPaths) {
		return record, nil
	}
	record.Cleanup, record.CleanupReason, record.KeptPaths = cleanup, reason, keptPaths
	if err := d.Registry.SetWorkCleanup(record.WorktreePath, cleanup, reason, keptPaths); err != nil {
		return record, err
	}
	if record.SessionID != "" {
		d.requestCatalogDiscovery()
	}
	return record, nil
}

func (d *Daemon) sessionWorking(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return false
	}
	return session.Status == surface.StatusBusy || surface.BackgroundRunning(session.BackgroundTasks)
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
		if _, err := d.Registry.SettleWork(sessionID, registry.WorkSettledPerson, time.Now()); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
	} else if record.Cleanup == registry.WorkCleanupKept {
		if err := d.Registry.SetWorkCleanup(record.WorktreePath, registry.WorkCleanupPending, "", nil); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
	}
	record, err = d.cleanWork(ctx, record.WorktreePath, options)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "sessionId": sessionID, "worktree": newWorkView(record)}), nil
}
