package daemon

import (
	"context"
	"time"
)

// Apps retry a send on their own for an hour and keep it for a tap retry, and
// a paired host replays its held work whenever the other host comes back, so
// a receipt has to outlast a long outage, not just the automatic retries.
const (
	actionReceiptRetention  = 30 * 24 * time.Hour
	actionReceiptPruneEvery = 5 * time.Minute
	actionReceiptPruneBatch = 500
	// A host's receipt for a forwarded key goes after this host's retention,
	// measured on its own clock; a day of margin covers the two clocks
	// disagreeing.
	heldReplayLimit = actionReceiptRetention - 24*time.Hour
)

func (d *Daemon) runActionReceiptPrunes(ctx context.Context) {
	ticker := time.NewTicker(actionReceiptPruneEvery)
	defer ticker.Stop()
	for {
		d.pruneActionReceipts(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) pruneActionReceipts(now time.Time) {
	if _, err := d.Registry.PruneActionReceipts(now.Add(-actionReceiptRetention), actionReceiptPruneBatch); err != nil {
		d.log.Printf("action receipts: prune: %s", err)
	}
}

// A held replay first sent longer ago than the host keeps receipts could run
// a second time there, so it is settled as unknown instead of sent.
func heldReplayTooOld(firstSent time.Time) bool {
	return !firstSent.IsZero() && time.Since(firstSent) > heldReplayLimit
}
