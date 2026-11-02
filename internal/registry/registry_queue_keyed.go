package registry

import (
	"database/sql"
	"errors"
)

func (r *Registry) CancelPendingByKey(deliveryKey string) (bool, error) {
	if deliveryKey == "" {
		return false, nil
	}
	var id int64
	var sessionID, message string
	err := r.db.QueryRow(`SELECT id,session_id,message FROM message_queue WHERE delivery_key=? AND status='pending'`, deliveryKey).Scan(&id, &sessionID, &message)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	res, err := r.db.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE id=? AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "no longer needed"})
	return true, r.ReconcileAttentionItems(r.now())
}
