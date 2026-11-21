package registry

import (
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type HeldSharedMessage struct {
	ID        int64
	SessionID string
	Holders   []surface.Holder
	raw       string
}

func (r *Registry) HeldSharedMessages() ([]HeldSharedMessage, error) {
	rows, err := r.db.Query(`SELECT id,session_id,holders_json FROM message_queue WHERE refused=1 AND status='pending' AND holders_json NOT IN ('','[]') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var held []HeldSharedMessage
	for rows.Next() {
		var message HeldSharedMessage
		if err := rows.Scan(&message.ID, &message.SessionID, &message.raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(message.raw), &message.Holders); err != nil {
			return nil, err
		}
		held = append(held, message)
	}
	return held, rows.Err()
}

// A message held for several terminals is free to send once at most one of them is left.
func (r *Registry) KeepHeldHolders(message HeldSharedMessage, left []surface.Holder, now time.Time) (bool, error) {
	if len(left) == len(message.Holders) {
		return false, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	holders, refused, reason := "[]", false, ""
	if len(left) > 1 {
		data, err := json.Marshal(left)
		if err != nil {
			return false, err
		}
		holders, refused, reason = string(data), true, surface.SharedConversationError{SessionID: message.SessionID, Holders: left}.HeldReason()
	}
	res, err := tx.Exec(`UPDATE message_queue SET holders_json=?1,refused=?2,available_at_ms=CASE WHEN ?2 THEN available_at_ms ELSE 0 END,last_error=CASE WHEN ?2 THEN ?5 ELSE last_error END,updated_at=datetime('now') WHERE id=?3 AND refused=1 AND status='pending' AND holders_json=?4`, holders, refused, message.ID, message.raw, reason)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	if err := syncDeliveryAsksTx(tx, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
