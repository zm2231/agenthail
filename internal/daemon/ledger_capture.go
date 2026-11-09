package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
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
}
