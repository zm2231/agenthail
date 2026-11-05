package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) personCapture(capture delivery.PersonCaptureContext) {
	if capture.IntentUUID == "" {
		return
	}
	now := time.Now()
	record, err := d.recordPersonWords(capture.Target, ledger.Utterance{
		Ref:        ledger.DeliveryRef(capture.IntentUUID),
		Surface:    string(capture.Target.Surface),
		Text:       capture.Message,
		At:         now,
		CapturedBy: ledger.CapturedByDelivery,
	})
	if err != nil {
		d.log.Printf("ledger capture: record delivery %s to %s in project %s: %s", capture.IntentUUID, capture.Target.ID, record.key, err)
		return
	}
	if !record.captured {
		return
	}
	d.delivered.add(capture.Target.ID, capture.IntentUUID, capture.Message, now)
	if record.attach {
		d.queueReadback(capture.Target, record.key, ledger.DeliveryRef(capture.IntentUUID))
	}
}

// Codex's prompt hook carries no prompt id, so it cannot hand the session its readback.
func (d *Daemon) queueReadback(target surface.Session, key, ref string) {
	if target.Surface != surface.KindCodex {
		return
	}
	text := ledger.QueuedReadbackInstruction(ref, key, d.unattachedBefore(key, target.ID, ref))
	if _, err := d.Registry.QueueMessageWithKey(target.ID, text, "ledger:readback:"+ref); err != nil {
		d.log.Printf("ledger capture: queue readback of %s for %s: %s", ref, target.ID, err)
	}
}
