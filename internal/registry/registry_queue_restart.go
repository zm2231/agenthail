package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type restartedIntent struct {
	id           int64
	status       DeliveryIntentStatus
	retries      int
	dismissed    bool
	notification sql.NullInt64
}

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
		if err := settleRestartedIntent(tx, intent, from, to, status, evidence, providerKey); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,relay_hops,turn_observed,updated_at) VALUES(?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET relay_hops=excluded.relay_hops,turn_observed=1,updated_at=datetime('now')`, to, relayHops); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(r.now())
}

func restartedIntents(tx *sql.Tx, queueID int64) ([]restartedIntent, error) {
	rows, err := tx.Query(`SELECT id,status,retries,dismissed_at IS NOT NULL,notification_queue_id FROM delivery_intents WHERE queue_id=? AND status!=?`, queueID, DeliveryIntentDelivered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []restartedIntent
	for rows.Next() {
		var intent restartedIntent
		if err := rows.Scan(&intent.id, &intent.status, &intent.retries, &intent.dismissed, &intent.notification); err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}

// The new conversation took the message, so every problem the old one raised about it is over, including notices not yet sent.
func settleRestartedIntent(tx *sql.Tx, intent restartedIntent, from, to string, status DeliveryIntentStatus, evidence surface.DeliveryEvidence, providerKey string) error {
	if _, err := tx.Exec(`UPDATE delivery_intents SET target_session_id=?,status=?,evidence=?,failure='',dismissed_at=NULL,notification_queue_id=NULL,provider_key=CASE WHEN ?!='' THEN ? ELSE provider_key END,updated_at=datetime('now') WHERE id=?`, to, status, evidence, providerKey, providerKey, intent.id); err != nil {
		return err
	}
	if intent.notification.Valid {
		if _, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE id=? AND status='pending'`, intent.notification.Int64); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE delivery_key=? AND status='pending'`, fmt.Sprintf("delivery-intent-held:%d:%d", intent.id, intent.retries)); err != nil {
		return err
	}
	if intent.dismissed || (intent.status != DeliveryIntentHeld && intent.status != DeliveryIntentFailed) {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"deliveryId": intent.id})
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("delivery.restarted:%d:%d", intent.id, intent.retries), Type: "delivery.dismissed", EntityID: from, Payload: payload, CreatedAt: time.Now().UTC()})
	return err
}
