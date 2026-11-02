package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

// A voided run must not hold the only checker slot until the timeout.
func (d *Daemon) releaseVoidCheck(ctx context.Context, store *ledger.Store, now time.Time) {
	r := d.checks.running
	if r == nil {
		return
	}
	board, err := store.Board(r.project)
	if err != nil {
		d.log.Printf("ledger checks: board of %s: %s", r.project, err)
		return
	}
	if _, ok := board.CheckRun(r.run); ok {
		return
	}
	target := r.checkTarget
	d.endChecker(ctx, r.run, now)
	d.publishEvent("ledger.check.void", r.run, map[string]string{"projectKey": target.project, "run": r.run, "reason": "it was given words reclassified as not the person's"})
	d.checks.enqueue(target, true)
	d.startNextCheck(ctx, store, now)
}
