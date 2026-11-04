package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	journalIdleAfter  = 7 * 24 * time.Hour
	journalSweepFirst = 10 * time.Minute
	journalSweepEvery = 24 * time.Hour
	journalSweepPause = 50 * time.Millisecond
)

func (d *Daemon) runJournalSweeps(ctx context.Context) {
	first := time.NewTimer(journalSweepFirst)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	d.sweepJournals(ctx, time.Now())
	ticker := time.NewTicker(journalSweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.sweepJournals(ctx, now)
		}
	}
}

func (d *Daemon) sweepJournals(ctx context.Context, now time.Time) {
	began := time.Now()
	sessions, err := d.Registry.IdleJournalSessions(now.Add(-journalIdleAfter))
	if err != nil {
		d.log.Printf("journal sweep: list idle sessions: %s", err)
		return
	}
	var total registry.JournalCompression
	for _, session := range sessions {
		if ctx.Err() != nil {
			return
		}
		compressed, err := d.Registry.CompressJournalSession(session, now.Add(-journalIdleAfter))
		if err != nil {
			d.log.Printf("journal sweep: session %s: %s", session, err)
		}
		total.Rows += compressed.Rows
		total.Plain += compressed.Plain
		total.Packed += compressed.Packed
		if !pauseJournalSweep(ctx) {
			return
		}
	}
	if total.Rows > 0 {
		d.log.Printf("journal sweep: %d rows in %d sessions, %d -> %d bytes, took %s", total.Rows, len(sessions), total.Plain, total.Packed, time.Since(began).Round(time.Millisecond))
	}
	d.reclaimJournalPages(ctx)
}

func (d *Daemon) reclaimJournalPages(ctx context.Context) {
	previous := int64(-1)
	for {
		left, err := d.Registry.ReclaimFreePages()
		if err != nil {
			d.log.Printf("journal sweep: reclaim free pages: %s", err)
			return
		}
		if left == 0 || left == previous || !pauseJournalSweep(ctx) {
			return
		}
		previous = left
	}
}

func pauseJournalSweep(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(journalSweepPause):
		return true
	}
}
