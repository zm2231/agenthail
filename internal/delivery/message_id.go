package delivery

import "github.com/zm2231/agenthail/internal/registry"

func (d Dispatcher) QueuedMessageID(deliveryID int64, sessionID string) string {
	if deliveryID == 0 {
		return ""
	}
	intent, err := d.Registry.DeliveryIntent(deliveryID)
	if err != nil {
		d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: sessionID, Error: "read the queued delivery's message id: " + err.Error()})
		return ""
	}
	return intent.UUID
}
