package registry

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// DeliveryTurnWatch is how long, from its transport accepting a delivery,
// the target is observed for the turn that reads it. A message nobody was
// seen reading is never a failure: it stays sent.
const DeliveryTurnWatch = time.Hour

const sqlNowMillis = `CAST((julianday('now')-2440587.5)*86400000 AS INTEGER)`

const awaitingTurnRow = `status=? AND evidence=? AND provider_key<>'' AND accepted_at_ms>0`

func awaitingTurnArgs(now time.Time) []any {
	return []any{DeliveryIntentSent, surface.EvidenceTransportAccepted, now.Add(-DeliveryTurnWatch).UnixMilli()}
}
