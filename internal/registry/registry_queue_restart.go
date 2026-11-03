package registry

import (
	"encoding/json"
	"fmt"
	"time"

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
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	var held []int64
	rows, err := tx.Query(`SELECT id FROM delivery_intents WHERE queue_id=? AND status=?`, id, DeliveryIntentHeld)
	if err != nil {
		return err
	}
	for rows.Next() {
		var intentID int64
		if err := rows.Scan(&intentID); err != nil {
			rows.Close()
			return err
		}
		held = append(held, intentID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET target_session_id=?,status=?,evidence=?,failure='',provider_key=CASE WHEN ?!='' THEN ? ELSE provider_key END,updated_at=datetime('now') WHERE queue_id=? AND status IN (?,?,?)`, to, status, evidence, providerKey, providerKey, id, DeliveryIntentQueued, DeliveryIntentUnknown, DeliveryIntentHeld); err != nil {
		return err
	}
	for _, intentID := range held {
		payload, err := json.Marshal(map[string]any{"deliveryId": intentID})
		if err != nil {
			return err
		}
		if _, _, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("delivery.restarted:%d", intentID), Type: "delivery.dismissed", EntityID: from, Payload: payload, CreatedAt: time.Now().UTC()}); err != nil {
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
