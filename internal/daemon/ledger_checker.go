package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	checkIdle    = 5 * time.Minute
	checkTimeout = 10 * time.Minute
)

type checkTarget struct {
	project string
	goal    string
}

type runningCheck struct {
	checkTarget
	run     string
	started time.Time
}

type checkerRunner struct {
	mu        sync.Mutex
	running   *runningCheck
	queue     []checkTarget
	recovered bool
}

func checkerDir(reg *registry.Registry) string {
	return filepath.Join(filepath.Dir(uploadsDir(reg)), "ledger-checker")
}

func mkdirPrivate(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

func (d *Daemon) sweepLedgerChecks(ctx context.Context, now time.Time) {
	store := d.ledgerStore()
	keys, err := store.ProjectsWith(ledger.EventGoal)
	if err != nil {
		d.log.Printf("ledger checks: list projects: %s", err)
		return
	}
	d.checks.mu.Lock()
	defer d.checks.mu.Unlock()
	if !d.checks.recovered {
		d.recoverChecks(ctx, store, keys, now)
		d.checks.recovered = true
	}
	if r := d.checks.running; r != nil && now.Sub(r.started) >= checkTimeout {
		d.finishCheck(ctx, store, r.project, r.run, ledger.CheckTimedOut, "no report within "+checkTimeout.String(), now)
	}
	for _, key := range keys {
		if enabled, err := store.Enabled(key); err != nil || !enabled {
			continue
		}
		board, err := store.Board(key)
		if err != nil {
			d.log.Printf("ledger checks: board of %s: %s", key, err)
			continue
		}
		for _, goal := range board.DueChecks(now, checkIdle) {
			d.checks.enqueue(checkTarget{project: key, goal: goal}, false)
		}
	}
	d.startNextCheck(ctx, store, now)
}

func (c *checkerRunner) enqueue(target checkTarget, first bool) bool {
	if c.running != nil && c.running.checkTarget == target {
		return false
	}
	for _, queued := range c.queue {
		if queued == target {
			return false
		}
	}
	if first {
		c.queue = append([]checkTarget{target}, c.queue...)
	} else {
		c.queue = append(c.queue, target)
	}
	return true
}

func (d *Daemon) recoverChecks(ctx context.Context, store *ledger.Store, keys []string, now time.Time) {
	for _, key := range keys {
		board, err := store.Board(key)
		if err != nil {
			continue
		}
		for _, run := range board.OpenChecks() {
			d.finishCheck(ctx, store, key, run.Run, ledger.CheckInterrupted, "the daemon restarted during the check", now)
		}
	}
}

func (d *Daemon) startNextCheck(ctx context.Context, store *ledger.Store, now time.Time) {
	for d.checks.running == nil && len(d.checks.queue) > 0 {
		target := d.checks.queue[0]
		d.checks.queue = d.checks.queue[1:]
		run := uuid.NewString()
		input, err := store.StartCheck(target.project, target.goal, run, now)
		var refusal *ledger.Refusal
		if errors.As(err, &refusal) {
			continue
		}
		if err != nil {
			d.log.Printf("ledger checks: start %s: %s", target.goal, err)
			return
		}
		if err := d.launchChecker(ctx, run, input); err != nil {
			d.log.Printf("ledger checks: launch for %s: %s", target.goal, err)
			d.finishCheck(ctx, store, target.project, run, ledger.CheckFailed, err.Error(), now)
			continue
		}
		d.checks.running = &runningCheck{checkTarget: target, run: run, started: now}
		d.publishEvent("ledger.check.started", run, map[string]string{"projectKey": target.project, "goalId": target.goal, "run": run})
	}
}

func (d *Daemon) launchChecker(ctx context.Context, run string, input ledger.CheckInput) error {
	starter, ok := d.adapters().ForSurface(surface.KindClaude).(surface.CheckerStarter)
	if !ok {
		return errors.New("the Claude host is not available to run the checker")
	}
	dir := checkerDir(d.Registry)
	if err := mkdirPrivate(dir); err != nil {
		return err
	}
	session, err := starter.StartChecker(ctx, surface.CheckerStart{Run: run, Cwd: dir, Message: checkerMessage(run, input)})
	if err != nil {
		return err
	}
	if err := d.Registry.RegisterSession(*session); err != nil {
		return err
	}
	return d.Registry.RecordSessionOrigin(registry.SessionOrigin{SessionID: run, Via: registry.OriginDaemon})
}

func (d *Daemon) finishCheck(ctx context.Context, store *ledger.Store, project, run, result, reason string, now time.Time) {
	if err := store.FinishCheck(project, run, result, reason, now); err != nil {
		d.log.Printf("ledger checks: finish %s: %s", run, err)
	}
	d.endChecker(ctx, run, now)
	d.publishEvent("ledger.check."+result, run, map[string]string{"projectKey": project, "run": run, "reason": reason})
}

// endChecker stops and archives only the session recorded for this run.
func (d *Daemon) endChecker(ctx context.Context, run string, now time.Time) {
	if r := d.checks.running; r != nil && r.run == run {
		d.checks.running = nil
	}
	session, err := d.Registry.Session(run)
	if err != nil {
		return
	}
	if lifecycle, ok := d.adapters().ForSurface(surface.KindClaude).(surface.SessionLifecycle); ok {
		if _, err := lifecycle.SessionAction(ctx, session, "stop"); err != nil {
			d.log.Printf("ledger checks: stop checker %s: %s", run, err)
		}
	}
	if err := d.Registry.ArchiveSession(run, now.UTC()); err != nil {
		d.log.Printf("ledger checks: archive checker %s: %s", run, err)
	}
}

func checkerMessage(run string, in ledger.CheckInput) string {
	var b strings.Builder
	b.WriteString("Check run " + run + ", goal " + in.GoalID + " (" + in.GoalName + ").\n\nThe person's words:\n")
	for _, u := range in.Utterances {
		b.WriteString("- " + u.ID + ": " + quoteBlock(u.Text) + "\n")
	}
	b.WriteString("\nClauses an agent wrote from them:\n")
	for _, c := range in.Clauses {
		b.WriteString("- " + c.ID + ": " + c.Text + " [" + string(c.State) + ", " + string(c.Source) + "]\n")
	}
	if len(in.Clauses) == 0 {
		b.WriteString("- none\n")
	}
	var pending []string
	for _, cut := range in.Cuts {
		if !cut.Acked && !cut.Withdrawn {
			pending = append(pending, "- "+cut.ID+": "+cut.Reason)
		}
	}
	if len(pending) > 0 {
		b.WriteString("\nAlready raised and still open (do not report these again):\n" + strings.Join(pending, "\n") + "\n")
	}
	b.WriteString("\nReport what the clauses miss with the report tool, once.")
	return b.String()
}

func quoteBlock(text string) string {
	return "\"" + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ") + "\""
}
