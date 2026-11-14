package registry

import (
	"database/sql"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) AckRestartedMessage(id int64, from, to string, relayHops int, evidence surface.DeliveryEvidence, providerKey string) error {
	if evidence == "" {
		return fmt.Errorf("delivery evidence is required")
	}
	status := DeliveryIntentSent
	if evidence == surface.EvidenceSubmitted {
		status = DeliveryIntentSubmitted
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET session_id=?,refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND session_id=? AND status='inflight'`, to, evidence, id, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return queueItemNotInflight(id)
	}
	intents, err := restartedIntents(tx, id)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if err := settleRestartedIntent(tx, intent, to, status, evidence, providerKey); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,relay_hops,turn_observed,updated_at) VALUES(?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET relay_hops=excluded.relay_hops,turn_observed=1,updated_at=datetime('now')`, to, relayHops); err != nil {
		return err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func restartedIntents(tx *sql.Tx, queueID int64) ([]int64, error) {
	rows, err := tx.Query(`SELECT id FROM delivery_intents WHERE queue_id=? AND status!=?`, queueID, DeliveryIntentDelivered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		intents = append(intents, id)
	}
	return intents, rows.Err()
}

func settleRestartedIntent(tx *sql.Tx, id int64, to string, status DeliveryIntentStatus, evidence surface.DeliveryEvidence, providerKey string) error {
	_, err := tx.Exec(`UPDATE delivery_intents SET target_session_id=?1,status=?2,evidence=?3,failure='',provider_key=CASE WHEN ?4!='' THEN ?4 ELSE provider_key END,accepted_at_ms=CASE WHEN ?2=?6 THEN `+sqlNowMillis+` ELSE 0 END,updated_at=datetime('now') WHERE id=?5`, to, status, evidence, providerKey, id, DeliveryIntentSent)
	return err
}
