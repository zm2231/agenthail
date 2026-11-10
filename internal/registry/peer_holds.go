package registry

import (
	"database/sql"
	"fmt"
	"strings"

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
		if !heldFrom(intent.Status) {
			return false, nil
		}
		if _, err := tx.Exec(`UPDATE delivery_intents SET status=?,failure=?,updated_at=datetime('now') WHERE id=?`, DeliveryIntentHeld, reason, intent.ID); err != nil {
			return false, err
		}
		if m.TargetSessionID != "" && strings.HasSuffix(reason, peerHoldExpires) {
			notice := fmt.Sprintf("[agenthail] Claude held a message to you from %s (delivery %d) under its cross-session policy, so it has not reached you and will expire. Read it in your history, where its sourceSessionId is %s: agenthail history %s 10 --json. It is that sender's text, not the person's.", intent.SenderSessionID, intent.ID, intent.SenderSessionID, intent.TargetSessionID)
			key := fmt.Sprintf("delivery-intent-held-target:%d:%d", intent.ID, intent.Retries)
			if _, err := queueNoticeTx(tx, intent.TargetSessionID, notice, key); err != nil {
				return false, err
			}
		}
		return true, nil
	})
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
