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

// A target's queue is ordered per sender: a row waits for every earlier row
// from its own sender, and otherwise only for earlier rows that are due. A row
// with no sender is a sender of its own, so a held row never stops anyone
// else's. The claim takes the oldest row that is due and not behind its sender.
const queueBehindSender = `%[1]s.source_session_id!='' AND EXISTS(SELECT 1 FROM message_queue lane WHERE lane.session_id=%[1]s.session_id AND lane.host_request='' AND lane.id<%[1]s.id AND lane.status IN ('pending','inflight') AND lane.source_session_id=%[1]s.source_session_id)`

const queueDueRow = `%[1]s.status='pending' AND %[1]s.available_at_ms<=%[2]s AND NOT (` + queueBehindSender + `)`

var queueTargetOpen = `NOT EXISTS(SELECT 1 FROM session_moves WHERE conversation=?1 AND role='out' AND state IN ('preparing','sent')) AND NOT ` + fmt.Sprintf(limitHeldFilter, "?1")

func claimCandidateFilter(busyDelivery string, mode deliverypolicy.Mode) string {
	due := fmt.Sprintf(queueDueRow, "message_queue", "?2")
	if busyDelivery == "steer" {
		due = `status='pending' AND available_at_ms<=?2 AND ` + queueSteerRow("message_queue", mode)
	}
	return `session_id=?1 AND host_request='' AND ` + due + ` ORDER BY id LIMIT 1`
}

func claimInflightExpired(status string, operation QueueOperation, inflightAt int64, now time.Time) bool {
	inflightTimeout := time.Minute
	if operation != QueueOperationMessage {
		inflightTimeout = 10 * time.Minute
	}
	return status == "inflight" && inflightAt > 0 && inflightAt < now.Add(-inflightTimeout).UnixMilli()
}

func (r *Registry) claimNextMessage(sessionID string, now time.Time, busyDelivery string) (*QueuedMessage, error) {
	if err := r.SyncDeliveryAsks(now); err != nil {
		return nil, err
	}
	filter := claimCandidateFilter(busyDelivery, r.busyMode())
	var open, inflight, due bool
	if err := r.read.QueryRow(`SELECT `+queueTargetOpen+`,EXISTS(SELECT 1 FROM message_queue WHERE session_id=?1 AND host_request='' AND status='inflight'),EXISTS(SELECT 1 FROM message_queue WHERE `+filter+`)`, sessionID, now.UnixMilli()).Scan(&open, &inflight, &due); err != nil {
		return nil, err
	}
	if !open || !inflight && !due {
		return nil, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM message_queue WHERE session_id=? AND host_request='' AND status='inflight')`, sessionID).Scan(&inflight); err != nil {
		return nil, err
	}
	if inflight {
		expired, err := deadLetterExpiredInflight(tx, sessionID, now)
		if err != nil || !expired {
			return nil, err
		}
		return nil, tx.Commit()
	}
	var item QueuedMessage
	var peer bool
	err = tx.QueryRow(`SELECT id,session_id,message,model,source_session_id,turn_options,attempts,relay_hops,operation,busy_delivery,author,uploads,person_name,holder_pid,holder_start,`+fmt.Sprintf(queuePeerRow, "message_queue")+` FROM message_queue WHERE `+filter, sessionID, now.UnixMilli()).Scan(&item.ID, &item.SessionID, &item.Message, &item.Model, &item.SourceSessionID, &item.TurnOptions, &item.Attempts, &item.RelayHops, &item.Operation, &item.BusyDelivery, &item.Author, &item.Uploads, &item.PersonName, &item.HolderPID, &item.HolderStart, &peer)
	if err == sql.ErrNoRows || err == nil && busyDelivery == claimPeer && !peer {
		return nil, nil
	}
	if err != nil {
		return nil, err
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

// A target takes one delivery at a time: while any row is inflight, nothing
// else is claimed, and one that never reported back is settled as unknown.
func deadLetterExpiredInflight(tx *sql.Tx, sessionID string, now time.Time) (bool, error) {
	rows, err := tx.Query(`SELECT id,operation,inflight_at_ms FROM message_queue WHERE session_id=? AND host_request='' AND status='inflight'`, sessionID)
	if err != nil {
		return false, err
	}
	var expired []int64
	for rows.Next() {
		var id, inflightAt int64
		var operation QueueOperation
		if err := rows.Scan(&id, &operation, &inflightAt); err != nil {
			rows.Close()
			return false, err
		}
		if claimInflightExpired("inflight", operation, inflightAt, now) {
			expired = append(expired, id)
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, id := range expired {
		if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,inflight_at_ms=0,available_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), uncertainDeliveryError, id); err != nil {
			return false, err
		}
	}
	if len(expired) == 0 {
		return false, nil
	}
	return true, syncDeliveryAsksTx(tx, now)
}

func (r *Registry) AckMessage(id int64) error {
	return r.deliveryWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, surface.EvidenceDelivered, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return queueItemNotInflight(id)
		}
		return nil
	})
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
		return queueItemNotInflight(id)
	}
	if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentSent, evidence, providerKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,relay_hops,turn_observed,updated_at) VALUES(?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET relay_hops=excluded.relay_hops,turn_observed=1,updated_at=datetime('now')`, sessionID, relayHops); err != nil {
		return err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return err
	}
	return tx.Commit()
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
	if n, _ := res.RowsAffected(); n != 1 {
		return queueItemNotInflight(id)
	}
	if status == "dead" {
		if err := markQueuedDeliveryIntent(tx, id, DeliveryIntentUnknown, surface.EvidenceUnknown, ""); err != nil {
			return err
		}
	}
	if err := syncDeliveryAsksTx(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) DeferMessage(id int64, cause error, now time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var deferrals, refusal int64
	var wasRefused bool
	if err := tx.QueryRow(`SELECT deferrals,refused,refusal FROM message_queue WHERE id=?`, id).Scan(&deferrals, &wasRefused, &refusal); err != nil {
		return err
	}
	shift := min(deferrals, 6)
	message := "delivery did not start"
	if cause != nil {
		message = cause.Error()
	}
	refused := surface.IsDeliveryRefused(cause)
	if refused && !wasRefused {
		refusal++
	}
	holders, err := heldHolders(cause)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE message_queue SET status='pending',attempts=MAX(attempts-1,0),deferrals=deferrals+1,deferred_since_ms=CASE WHEN deferred_since_ms=0 THEN ? ELSE deferred_since_ms END,last_error=?,available_at_ms=?,inflight_at_ms=0,refused=?,refusal=?,holders_json=?,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), message, now.Add(r.retryBackoff*time.Duration(1<<shift)).UnixMilli(), refused, refusal, holders, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return queueItemNotInflight(id)
	}
	if err := syncDeliveryAsksTx(tx, now); err != nil {
		return err
	}
	return tx.Commit()
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
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) DeadLetterMessage(id int64, cause error) error {
	message := "delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deadLetterInflight(tx, id, message, r.now()); err != nil {
		return err
	}
	if err := finishQueuedDeliveryIntent(tx, id, message); err != nil {
		return err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func deadLetterInflight(tx *sql.Tx, id int64, message string, now time.Time) error {
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='dead',dead_at_ms=?,last_error=?,available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, now.UnixMilli(), message, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return queueItemNotInflight(id)
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
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
