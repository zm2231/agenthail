package registry

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// PeerMessage names a message sent over Claude's peer socket by its id, which
// is its delivery's uuid, and the side that reported on it: its target's host
// or its sender's peer worker.
type PeerMessage struct {
	MsgID           string
	TargetSessionID string
	SenderSessionID string
}

// HoldPeerMessage records that the target's Claude parked the message under
// its inbound policy. The message stays a problem until Claude releases or
// drops it.
func (r *Registry) HoldPeerMessage(m PeerMessage, reason string) (bool, error) {
	if strings.TrimSpace(reason) == "" {
		return false, fmt.Errorf("a held message needs its reason")
	}
	return r.peerTransition(m, func(tx *sql.Tx, intent *DeliveryIntent) (bool, error) {
		return holdIntentTx(tx, intent, reason, r.now())
	})
}

func holdIntentTx(tx *sql.Tx, intent *DeliveryIntent, reason string, now time.Time) (bool, error) {
	if !heldFrom(intent.Status) {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET status=?,failure=?,held_at_ms=?,updated_at=datetime('now') WHERE id=?`, DeliveryIntentHeld, reason, now.UnixMilli(), intent.ID); err != nil {
		return false, err
	}
	if strings.HasSuffix(reason, peerHoldExpires) {
		notice := fmt.Sprintf("[agenthail] Claude held a message to you from %s (delivery %d) under its cross-session policy, so it has not reached you and will expire. Read it in your history, where its sourceSessionId is %s: agenthail history %s 10 --json. It is that sender's text, not the person's.", intent.SenderSessionID, intent.ID, intent.SenderSessionID, intent.TargetSessionID)
		key := fmt.Sprintf("delivery-intent-held-target:%d:%d", intent.ID, intent.Retries)
		if _, err := queueNoticeTx(tx, intent.TargetSessionID, notice, key); err != nil {
			return false, err
		}
	}
	return true, nil
}

// PeerHoldMatch is what a hold report that names no delivery matched among
// the deliveries from its sender to its target.
type PeerHoldMatch int

const (
	PeerHoldUnmatched PeerHoldMatch = iota
	PeerHoldMatched
	PeerHoldAmbiguous
)

// PeerMessageKnown reports whether m names a delivery to its target.
func (r *Registry) PeerMessageKnown(m PeerMessage) (bool, error) {
	var known bool
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_intents WHERE uuid=? AND uuid!='' AND target_session_id=?)`, strings.TrimSpace(m.MsgID), m.TargetSessionID).Scan(&known)
	return known, err
}

// HoldPeerMessageFromSender records a hold that Claude reported without
// naming the delivery, by its sender and target. Only a delivery sent by
// heldAt can be the one held, so a report replayed later never reaches a
// message sent after it. Claude reports a hold before the sender's send
// returns, so a send still in flight counts. It holds the delivery only when
// exactly one from that sender to that target is still awaiting its turn. With several it never
// picks one: each is marked as one that may have been held, which ends as soon
// as it resolves either way.
func (r *Registry) HoldPeerMessageFromSender(senderSessionID, targetSessionID string, heldAt time.Time, reason string) (PeerHoldMatch, error) {
	if senderSessionID == "" || targetSessionID == "" || heldAt.IsZero() || strings.TrimSpace(reason) == "" {
		return PeerHoldUnmatched, fmt.Errorf("a hold by sender needs its sender, target, time and reason")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return PeerHoldUnmatched, err
	}
	defer tx.Rollback()
	ids, err := peerPairIntents(tx, senderSessionID, targetSessionID, heldAt, `(status=? OR status=? AND evidence=?)`, DeliveryIntentSubmitted, DeliveryIntentSent, surface.EvidenceTransportAccepted)
	if err != nil {
		return PeerHoldUnmatched, err
	}
	match := PeerHoldUnmatched
	switch {
	case len(ids) == 1:
		intent, err := deliveryIntentByID(tx, ids[0])
		if err != nil {
			return PeerHoldUnmatched, err
		}
		changed, err := holdIntentTx(tx, intent, strings.TrimSpace(reason), r.now())
		if err != nil || !changed {
			return PeerHoldUnmatched, err
		}
		match = PeerHoldMatched
	case len(ids) > 1:
		var target string
		if err := tx.QueryRow(`SELECT CASE WHEN COALESCE(name,'')!='' THEN name ELSE id END FROM sessions WHERE id=?`, targetSessionID).Scan(&target); err != nil && err != sql.ErrNoRows {
			return PeerHoldUnmatched, err
		}
		if target == "" {
			target = targetSessionID
		}
		maybe := fmt.Sprintf("One of these %d messages to %s may have been held; Claude did not say which. %s", len(ids), target, strings.TrimSpace(reason))
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE delivery_intents SET failure=?,updated_at=datetime('now') WHERE id=?`, strings.TrimSpace(maybe), id); err != nil {
				return PeerHoldUnmatched, err
			}
		}
		match = PeerHoldAmbiguous
	default:
		return PeerHoldUnmatched, nil
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return PeerHoldUnmatched, err
	}
	return match, tx.Commit()
}

// DropPeerMessageFromSender records that Claude dropped a message it held,
// reported without naming the delivery. Only the deliveries from that sender
// to that target already held, or possibly held, can be the one; it fails the
// delivery when there is exactly one, and otherwise changes nothing, since
// each is already a problem the person sees.
func (r *Registry) DropPeerMessageFromSender(senderSessionID, targetSessionID string, droppedAt time.Time, reason string) (bool, error) {
	if senderSessionID == "" || targetSessionID == "" || droppedAt.IsZero() {
		return false, fmt.Errorf("a drop by sender needs its sender, target and time")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	ids, err := peerPairIntents(tx, senderSessionID, targetSessionID, droppedAt, `(status=? OR status IN (?,?) AND failure!='')`, DeliveryIntentHeld, DeliveryIntentSubmitted, DeliveryIntentSent)
	if err != nil || len(ids) != 1 {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET status=? WHERE id=?`, DeliveryIntentHeld, ids[0]); err != nil {
		return false, err
	}
	if changed, err := failDeliveryIntent(tx, ids[0], reason); err != nil || !changed {
		return false, err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func peerPairIntents(tx *sql.Tx, senderSessionID, targetSessionID string, at time.Time, where string, args ...any) ([]int64, error) {
	query := `SELECT id FROM delivery_intents WHERE sender_session_id=? AND target_session_id=? AND strftime('%Y-%m-%d %H:%M:%f',created_at)<=strftime('%Y-%m-%d %H:%M:%f',?) AND ` + where + ` ORDER BY id`
	rows, err := tx.Query(query, append([]any{senderSessionID, targetSessionID, formatTimestamp(at)}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReleasePeerMessage records that Claude queued a held message for its model
// after all, which takes it off the problem list. It counts as delivered, so a
// host replaying its events cannot hold it again, and its queued retry is
// settled so the outbox cannot send it a second time.
func (r *Registry) ReleasePeerMessage(m PeerMessage) (bool, error) {
	changed, err := r.peerTransition(m, func(tx *sql.Tx, intent *DeliveryIntent) (bool, error) {
		if intent.Status != DeliveryIntentHeld {
			return false, nil
		}
		if _, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure='',updated_at=datetime('now') WHERE id=?`, DeliveryIntentDelivered, surface.EvidenceDelivered, intent.ID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`UPDATE message_queue SET refused=0,status='delivered',evidence=?,delivered=1,last_error='',inflight_at_ms=0,updated_at=datetime('now') WHERE id=(SELECT queue_id FROM delivery_intents WHERE id=?) AND status IN ('pending','inflight')`, surface.EvidenceDelivered, intent.ID); err != nil {
			return false, err
		}
		_, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE delivery_key=? AND status='pending'`, fmt.Sprintf("delivery-intent-held-target:%d:%d", intent.ID, intent.Retries))
		return true, err
	})
	return changed, err
}

// DropPeerMessage records that Claude will never pass the message to its
// model: a failure the sender is told about.
func (r *Registry) DropPeerMessage(m PeerMessage, reason string) (bool, error) {
	return r.peerTransition(m, func(tx *sql.Tx, intent *DeliveryIntent) (bool, error) {
		if !heldFrom(intent.Status) && intent.Status != DeliveryIntentHeld {
			return false, nil
		}
		if _, err := tx.Exec(`UPDATE delivery_intents SET status=? WHERE id=?`, DeliveryIntentHeld, intent.ID); err != nil {
			return false, err
		}
		return failDeliveryIntent(tx, intent.ID, reason)
	})
}

// heldFrom is every state a message Claude took can be in when Claude reports
// on it: the report can arrive before the sender records the send.
func heldFrom(status DeliveryIntentStatus) bool {
	return status == DeliveryIntentSubmitted || status == DeliveryIntentQueued || status == DeliveryIntentUnknown || status == DeliveryIntentSent
}

func (r *Registry) peerTransition(m PeerMessage, apply func(*sql.Tx, *DeliveryIntent) (bool, error)) (bool, error) {
	if strings.TrimSpace(m.MsgID) == "" || m.TargetSessionID == "" && m.SenderSessionID == "" {
		return false, fmt.Errorf("a peer message needs its id and its target or sender")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id int64
	if m.TargetSessionID != "" {
		err = tx.QueryRow(`SELECT id FROM delivery_intents WHERE uuid=? AND target_session_id=?`, m.MsgID, m.TargetSessionID).Scan(&id)
	} else {
		err = tx.QueryRow(`SELECT id FROM delivery_intents WHERE uuid=? AND sender_session_id=?`, m.MsgID, m.SenderSessionID).Scan(&id)
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	intent, err := deliveryIntentByID(tx, id)
	if err != nil {
		return false, err
	}
	changed, err := apply(tx, intent)
	if err != nil || !changed {
		return false, err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// PeerHeldReason says in plain words why a hosted session's Claude held a
// message, from the cause Claude reports.
const peerHoldExpires = " A hosted session has no prompt to approve it, so it expires."

func PeerHeldReason(cause string) string {
	const unapproved = peerHoldExpires
	switch cause {
	case "no-mode-asserted":
		return "Claude held this message: the session runs without permission prompts and the sender did not state that it does too." + unapproved
	case "mode-mismatch":
		return "Claude held this message: the sender runs with a different permission mode than the session." + unapproved
	case "explicit-setting", "managed-setting", "repo-setting", "bypass-default":
		return "Claude held this message: the session's crossSessionInbound setting holds messages from other sessions." + unapproved
	case "invalid-setting":
		return "Claude held this message: a settings file has an unrecognized crossSessionInbound value." + unapproved
	case "mode-unknown":
		return "Claude held this message until the session finishes starting."
	}
	return "Claude held this message under its cross-session policy." + unapproved
}

// PeerDroppedReason says in plain words why Claude dropped a message.
func PeerDroppedReason(outcome string) string {
	switch outcome {
	case "expired":
		return "Claude dropped this message: nobody approved it before its deadline."
	case "refused", "denied":
		return "Claude refused this message under its cross-session policy."
	}
	return "Claude dropped this message without passing it to the model."
}

// PeerHoldUnreported is the failure of a held message whose drop or release
// Claude never reported by its deadline.
const PeerHoldUnreported = "Claude held this message; its outcome was never reported by the hold's deadline."

type HeldPeerMessage struct {
	ID              int64
	TargetSessionID string
	HeldAt          time.Time
}

func (r *Registry) HeldPeerMessages() ([]HeldPeerMessage, error) {
	rows, err := r.db.Query(`SELECT id,target_session_id,held_at_ms FROM delivery_intents WHERE status=? AND held_at_ms>0 ORDER BY id`, DeliveryIntentHeld)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var held []HeldPeerMessage
	for rows.Next() {
		var message HeldPeerMessage
		var heldAt int64
		if err := rows.Scan(&message.ID, &message.TargetSessionID, &heldAt); err != nil {
			return nil, err
		}
		message.HeldAt = time.UnixMilli(heldAt)
		held = append(held, message)
	}
	return held, rows.Err()
}

// FailUnreportedPeerHold fails m when it is still held from the same hold.
func (r *Registry) FailUnreportedPeerHold(m HeldPeerMessage) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var held bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_intents WHERE id=? AND status=? AND held_at_ms=?)`, m.ID, DeliveryIntentHeld, m.HeldAt.UnixMilli()).Scan(&held); err != nil || !held {
		return false, err
	}
	if changed, err := failDeliveryIntent(tx, m.ID, PeerHoldUnreported); err != nil || !changed {
		return false, err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
