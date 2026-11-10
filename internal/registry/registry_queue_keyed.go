package registry

import (
	"database/sql"
	"errors"
)

func (r *Registry) DeliveryKeyUsed(deliveryKey string) (bool, error) {
	return r.readExists(`SELECT EXISTS(SELECT 1 FROM message_queue WHERE delivery_key=? AND delivery_key!='')`, deliveryKey)
}

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
	canceled := false
	err = r.deliveryWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE id=? AND status='pending'`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		canceled = n == 1
		return nil
	})
	if err != nil || !canceled {
		return false, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "no longer needed"})
	return true, nil
}
