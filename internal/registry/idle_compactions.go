package registry

import (
	"database/sql"
	"time"
)

type IdleCompactionOutcome string

const (
	IdleCompactionQueued    IdleCompactionOutcome = "queued"
	IdleCompactionAttempted IdleCompactionOutcome = "attempted"
	IdleCompactionWaiting   IdleCompactionOutcome = "waiting"
)

// QueueIdleCompaction queues one compact for the idle window that began with
// the session's request at window. It queues nothing when the window already
// had its attempt or anything waits in the session's queue, so a compact
// never runs ahead of a message.
func (r *Registry) QueueIdleCompaction(sessionID string, window time.Time) (IdleCompactionOutcome, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var attempted int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM idle_compactions WHERE session_id=? AND window_ms=?`, sessionID, window.UnixMilli()).Scan(&attempted); err != nil {
		return "", err
	}
	if attempted > 0 {
		return IdleCompactionAttempted, nil
	}
	var waiting int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, sessionID).Scan(&waiting); err != nil {
		return "", err
	}
	if waiting > 0 {
		return IdleCompactionWaiting, nil
	}
	if _, err := tx.Exec(`INSERT INTO idle_compactions(session_id,window_ms) VALUES(?,?)`, sessionID, window.UnixMilli()); err != nil {
		return "", err
	}
	id, _, err := queueCompactTx(tx, sessionID)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE idle_compactions SET queue_id=? WHERE session_id=? AND window_ms=?`, id, sessionID, window.UnixMilli()); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: "idle compact"})
	return IdleCompactionQueued, nil
}

func (r *Registry) IsIdleCompaction(queueID int64) (bool, error) {
	var found int
	err := r.read.QueryRow(`SELECT 1 FROM idle_compactions WHERE queue_id=?`, queueID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (r *Registry) RecordIdleCompactionRequest(queueID int64, requestUUID string) error {
	_, err := r.db.Exec(`UPDATE idle_compactions SET request_uuid=? WHERE queue_id=?`, requestUUID, queueID)
	return err
}

func (r *Registry) IdleCompactionRequests(sessionID string) (map[string]bool, error) {
	rows, err := r.read.Query(`SELECT request_uuid FROM idle_compactions WHERE session_id=? AND request_uuid!=''`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := map[string]bool{}
	for rows.Next() {
		var request string
		if err := rows.Scan(&request); err != nil {
			return nil, err
		}
		requests[request] = true
	}
	return requests, rows.Err()
}
