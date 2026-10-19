package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) coordinator() coordinator.Service {
	return coordinator.Service{Registry: d.Registry, Adapters: d.adapters(), Dispatcher: d.dispatcher()}
}

// restartCoordinator delivers a queued message by starting the coordinator
// again when its conversation is gone. It reports whether it handled item.
func (d *Daemon) restartCoordinator(ctx context.Context, session *surface.Session, item *registry.QueuedMessage, now time.Time) bool {
	coord := d.coordinator()
	if current, replaced := coord.ReplacedBy(session.ID); replaced {
		if err := d.Registry.MoveClaimedMessage(item.ID, current); err != nil {
			d.log.Printf("move queue item %d to coordinator %s: %s", item.ID, current, err)
			if _, err := d.Registry.DeferMessage(item.ID, err, now); err != nil {
				d.log.Printf("defer queue item %d: %s", item.ID, err)
			}
		}
		return true
	}
	composed, _ := item.Uploads.Compose(item.Message, false)
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	outcome, handled, err := coord.Restart(operationCtx, session, composed)
	if !handled {
		return false
	}
	if err != nil {
		d.finishQueueFailure(item, session, err, now)
		return true
	}
	receipt := outcome.Receipt
	if err := d.Registry.AckMessageWithEvidence(item.ID, session.ID, item.RelayHops, receipt.Evidence, receipt.TurnID); err != nil {
		d.log.Printf("ack queue item %d: %s", item.ID, err)
		return true
	}
	d.log.Printf("delivered queue item %d by starting coordinator %s: %s", item.ID, outcome.Session.ID, outcome.Reason)
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "delivered", SessionID: outcome.Session.ID, QueueID: item.ID, Message: item.Message, Result: receipt.TurnID, Evidence: receipt.Evidence})
	return true
}
