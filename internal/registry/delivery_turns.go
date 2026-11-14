package registry

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// DeliveryTurnWatch is how long a delivery that only reached its target's
// transport waits, from that acceptance, for a turn that reads it before it
// fails as unseen. A delivery accepted before acceptance was recorded has no
// clock and is left as it is.
const DeliveryTurnWatch = time.Hour

const DeliveryTurnUnseen = "Claude accepted this message, but no turn that read it was seen within an hour."

const sqlNowMillis = `CAST((julianday('now')-2440587.5)*86400000 AS INTEGER)`

const awaitingTurnRow = `status=? AND evidence=? AND provider_key<>'' AND accepted_at_ms>0`

func awaitingTurnArgs(now time.Time) []any {
	return []any{DeliveryIntentSent, surface.EvidenceTransportAccepted, now.Add(-DeliveryTurnWatch).UnixMilli()}
}

// FailUnseenDeliveryTurns fails each delivery that only reached a local
// target's transport more than DeliveryTurnWatch ago with no turn seen reading
// it, unless it is already listed as one its target may have held.
func (r *Registry) FailUnseenDeliveryTurns(now time.Time) (int64, error) {
	const unseen = `FROM delivery_intents WHERE ` + awaitingTurnRow + ` AND accepted_at_ms<? AND failure='' AND target_session_id IN (SELECT id FROM sessions WHERE host_id='')`
	if any, err := r.readExists(`SELECT EXISTS(SELECT 1 `+unseen+`)`, awaitingTurnArgs(now)...); err != nil || !any {
		return 0, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE delivery_intents SET failed_from=status,status=?,evidence=?,failure=?,updated_at=datetime('now') WHERE id IN (SELECT id `+unseen+`)`, append([]any{DeliveryIntentFailed, surface.EvidenceFailed, DeliveryTurnUnseen}, awaitingTurnArgs(now)...)...)
	if err != nil {
		return 0, err
	}
	failed, err := res.RowsAffected()
	if err != nil || failed == 0 {
		return 0, err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return 0, err
	}
	return failed, tx.Commit()
}
