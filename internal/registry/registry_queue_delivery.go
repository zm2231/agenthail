package registry

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) ClaimNextMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	return r.claimNextMessage(sessionID, now, "")
}

func (r *Registry) ClaimNextSteerMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	return r.claimNextMessage(sessionID, now, "steer")
}

func (r *Registry) ClaimNextPeerMessage(sessionID string, now time.Time) (*QueuedMessage, error) {
	return r.claimNextMessage(sessionID, now, claimPeer)
}

const claimPeer = "peer"

const queuePeerRow = `%[1]s.operation='message' AND %[1]s.author!='person' AND %[1]s.busy_delivery='' AND %[1]s.model='' AND (%[1]s.turn_options='' OR %[1]s.turn_options='{}') AND (%[1]s.uploads='' OR %[1]s.uploads='[]')`

func claimCandidateFilter(sessionID, busyDelivery string, mode deliverypolicy.Mode) (string, []any) {
	filter := `session_id=? AND host_request='' AND status IN ('pending','inflight') AND NOT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent')) AND NOT ` + fmt.Sprintf(limitHeldFilter, "?")
	args := []any{sessionID, sessionID, sessionID}
	if busyDelivery == claimPeer {
		filter += ` AND id=(SELECT MIN(m.id) FROM message_queue m WHERE m.session_id=? AND m.host_request='' AND m.status IN ('pending','inflight')) AND ` + fmt.Sprintf(queuePeerRow, "message_queue")
		args = append(args, sessionID)
	} else if busyDelivery == "steer" {
		filter += ` AND ` + queueSteerRow("message_queue", mode)
	}
	return filter + ` ORDER BY id LIMIT 1`, args
}

func claimInflightExpired(status string, operation QueueOperation, inflightAt int64, now time.Time) bool {
	inflightTimeout := time.Minute
	if operation != QueueOperationMessage {
		inflightTimeout = 10 * time.Minute
	}
	return status == "inflight" && inflightAt > 0 && inflightAt < now.Add(-inflightTimeout).UnixMilli()
}

func claimNeedsWrite(status string, operation QueueOperation, availableAt, inflightAt int64, now time.Time) bool {
	return claimInflightExpired(status, operation, inflightAt, now) || status == "pending" && availableAt <= now.UnixMilli()
}

func (r *Registry) claimNextMessage(sessionID string, now time.Time, busyDelivery string) (*QueuedMessage, error) {
	if err := r.ReconcileAttentionItems(now); err != nil {
		return nil, err
	}
	mode, err := r.busyMode()
	if err != nil {
		return nil, err
	}
	filter, args := claimCandidateFilter(sessionID, busyDelivery, mode)
	var peekStatus string
	var peekOperation QueueOperation
	var peekAvailableAt, peekInflightAt int64
	err = r.read.QueryRow(`SELECT status,available_at_ms,inflight_at_ms,operation FROM message_queue WHERE `+filter, args...).Scan(&peekStatus, &peekAvailableAt, &peekInflightAt, &peekOperation)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !claimNeedsWrite(peekStatus, peekOperation, peekAvailableAt, peekInflightAt, now) {
		return nil, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var item QueuedMessage
	var status string
	var availableAt int64
	var inflightAt int64
	err = tx.QueryRow(`SELECT id,session_id,message,model,source_session_id,turn_options,attempts,relay_hops,status,available_at_ms,inflight_at_ms,operation,busy_delivery,author,uploads,person_name FROM message_queue WHERE `+filter, args...).Scan(&item.ID, &item.SessionID, &item.Message, &item.Model, &item.SourceSessionID, &item.TurnOptions, &item.Attempts, &item.RelayHops, &status, &availableAt, &inflightAt, &item.Operation, &item.BusyDelivery, &item.Author, &item.Uploads, &item.PersonName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if claimInflightExpired(status, item.Operation, inflightAt, now) {
		if err := markQueuedDeliveryIntent(tx, item.ID, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), uncertainDeliveryError, item.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, r.ReconcileAttentionItems(now)
	}
	if status != "pending" || availableAt > now.UnixMilli() {
		return nil, nil
	}
	res, err := tx.Exec(`UPDATE message_queue SET status='inflight',attempts=attempts+1,inflight_at_ms=?,updated_at=datetime('now') WHERE id=? AND status='pending'`, now.UnixMilli(), item.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, fmt.Errorf("queue item %d was claimed concurrently", item.ID)
	}
	item.Attempts++
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *Registry) AckMessage(id int64) error {
	res, err := r.db.Exec(`UPDATE message_queue SET refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, surface.EvidenceDelivered, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) AckMessageWithRelayHops(id int64, sessionID string, relayHops int) error {
	return r.AckMessageWithEvidence(id, sessionID, relayHops, surface.EvidenceDelivered, "")
}

func (r *Registry) AckMessageWithEvidence(id int64, sessionID string, relayHops int, evidence surface.DeliveryEvidence, providerKey string) error {
	if evidence == "" {
		return fmt.Errorf("delivery evidence is required")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND session_id=? AND status='inflight'`, evidence, id, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentSent, evidence, providerKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,relay_hops,turn_observed,updated_at) VALUES(?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET relay_hops=excluded.relay_hops,turn_observed=1,updated_at=datetime('now')`, sessionID, relayHops); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) NackMessage(id int64, cause error, now time.Time, maxAttempts int) error {
	var attempts int
	if err := r.db.QueryRow(`SELECT attempts FROM message_queue WHERE id=?`, id).Scan(&attempts); err != nil {
		return err
	}
	status := "pending"
	available := now
	if attempts >= maxAttempts {
		status = "dead"
	} else {
		shift := attempts - 1
		if shift < 0 {
			shift = 0
		}
		if shift > 6 {
			shift = 6
		}
		available = now.Add(r.retryBackoff * time.Duration(1<<shift))
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	deadAt := int64(0)
	if status == "dead" {
		deadAt = now.UnixMilli()
	}
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status=?,dead_at_ms=?,last_error=?,available_at_ms=?,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, status, deadAt, message, available.UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 && status == "dead" {
		if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(now)
}

func (r *Registry) DeferMessage(id int64, cause error, now time.Time) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var deferrals, refusal, announced int64
	var wasRefused bool
	if err := tx.QueryRow(`SELECT deferrals,refused,refusal,refusal_announced FROM message_queue WHERE id=?`, id).Scan(&deferrals, &wasRefused, &refusal, &announced); err != nil {
		return 0, err
	}
	shift := min(deferrals, 6)
	message := "delivery did not start"
	if cause != nil {
		message = cause.Error()
	}
	refused := surface.IsDeliveryRefused(cause)
	began := refused && !wasRefused
	if began {
		refusal++
	}
	res, err := tx.Exec(`UPDATE message_queue SET status='pending',attempts=MAX(attempts-1,0),deferrals=deferrals+1,last_error=?,available_at_ms=?,inflight_at_ms=0,refused=?,refusal=?,updated_at=datetime('now') WHERE id=? AND status='inflight'`, message, now.Add(r.retryBackoff*time.Duration(1<<shift)).UnixMilli(), refused, refusal, id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, tx.Commit()
	}
	if began {
		if err := queueDeliveryRefusalNotice(tx, id, refusal, message); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if !refused || announced == refusal {
		refusal = 0
	}
	return refusal, r.ReconcileAttentionItems(now)
}

func (r *Registry) MarkRefusalAnnounced(id, refusal int64) error {
	_, err := r.db.Exec(`UPDATE message_queue SET refusal_announced=? WHERE id=? AND refusal=?`, refusal, id, refusal)
	return err
}

func (r *Registry) DeadLetterUnknown(id int64, cause error) error {
	message := uncertainDeliveryError
	if cause != nil {
		message = fmt.Sprintf("delivery outcome is unknown: %s; retry explicitly if the target did not receive it", cause)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deadLetterInflight(tx, id, message, r.now()); err != nil {
		return err
	}
	if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) DeadLetterMessage(id int64, cause error) (bool, error) {
	message := "delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := deadLetterInflight(tx, id, message, r.now()); err != nil {
		return false, err
	}
	notified, err := finishQueuedDeliveryIntent(tx, id, message)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if err := r.ReconcileAttentionItems(r.now()); err != nil {
		return notified, err
	}
	return notified, nil
}

func deadLetterInflight(tx *sql.Tx, id int64, message string, now time.Time) error {
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), message, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	return nil
}

func (r *Registry) RecoverInflight(before time.Time) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,updated_at=datetime('now') WHERE status=? AND queue_id IN (SELECT id FROM message_queue WHERE status='inflight' AND inflight_at_ms<? AND host_request='')`, DeliveryIntentUnknown, surface.EvidenceUnknown, DeliveryIntentQueued, before.UnixMilli()); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE status='inflight' AND inflight_at_ms<? AND host_request=''`, r.now().UnixMilli(), uncertainDeliveryError, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if n > 0 {
		if err := r.ReconcileAttentionItems(before); err != nil {
			return n, err
		}
	}
	return n, nil
}
