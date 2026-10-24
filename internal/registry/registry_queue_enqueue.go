package registry

import (
	"database/sql"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) QueueMessage(sessionID, message string) error {
	_, err := r.QueueMessageWithKey(sessionID, message, "")
	return err
}

func (r *Registry) QueueMessageWithKey(sessionID, message, deliveryKey string) (int64, error) {
	return r.QueueMessageWithOptions(sessionID, message, deliveryKey, surface.SendOptions{})
}

func (r *Registry) QueueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions) (int64, error) {
	return r.queueMessageWithOptions(sessionID, message, deliveryKey, options, 0, QueueOperationMessage)
}

func (r *Registry) QueueDeliveryWithIntent(sessionID, message, deliveryKey string, options surface.SendOptions) (int64, int64, error) {
	return r.enqueueMessage(sessionID, message, deliveryKey, options, 0, QueueOperationMessage, true)
}

type QueueOperation string

const (
	QueueOperationMessage QueueOperation = "message"
	QueueOperationCompact QueueOperation = "compact"
	QueueOperationGoal    QueueOperation = "goal"
)

func (r *Registry) QueueGoal(sessionID, command string) (int64, error) {
	res, err := r.db.Exec(`INSERT INTO message_queue (session_id,message,operation,status,updated_at) VALUES (?,?,?,'pending',datetime('now'))`, sessionID, command, QueueOperationGoal)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: command})
	return id, nil
}

func (r *Registry) QueueCompact(sessionID string) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRow(`SELECT id FROM message_queue WHERE session_id=? AND operation=? AND status IN ('pending','inflight') ORDER BY id LIMIT 1`, sessionID, QueueOperationCompact).Scan(&existing)
	if err == nil {
		return existing, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,status,updated_at) VALUES (?,?,?,'pending',datetime('now'))`, sessionID, "/compact", QueueOperationCompact)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: "compact"})
	return id, nil
}

func (r *Registry) QueueRelayMessage(sessionID, message, deliveryKey string, relayHops int) (int64, error) {
	return r.QueueRelayMessageWithOptions(sessionID, message, deliveryKey, relayHops, surface.SendOptions{})
}

func (r *Registry) QueueRelayMessageWithOptions(sessionID, message, deliveryKey string, relayHops int, options surface.SendOptions) (int64, error) {
	id, _, err := r.enqueueMessage(sessionID, message, deliveryKey, options, relayHops, QueueOperationMessage, true)
	return id, err
}

func (r *Registry) queueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation) (int64, error) {
	id, _, err := r.enqueueMessage(sessionID, message, deliveryKey, options, relayHops, operation, false)
	return id, err
}

func (r *Registry) enqueueMessage(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, recordIntent bool) (int64, int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	id, deliveryID, err := enqueueMessageTx(tx, r.now(), sessionID, message, deliveryKey, options, relayHops, operation, recordIntent)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, SourceSessionID: options.SourceSessionID, QueueID: id, Message: message, Result: options.Model})
	return id, deliveryID, nil
}

func enqueueMessageTx(tx *sql.Tx, now time.Time, sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, recordIntent bool) (int64, int64, error) {
	if options.BusyDelivery == "steer" && options.HoldsForQueue() {
		options.BusyDelivery = "queue"
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,delivery_key,model,source_session_id,turn_options,relay_hops,busy_delivery,author,uploads,person_name,status,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'pending',datetime('now'))`, sessionID, message, operation, deliveryKey, options.Model, options.SourceSessionID, options.TurnOptions, relayHops, options.BusyDelivery, options.Author, options.Uploads, options.PersonName)
	if err != nil {
		if deliveryKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			var id int64
			if err := tx.QueryRow(`SELECT id FROM message_queue WHERE delivery_key=? AND delivery_key!=''`, deliveryKey).Scan(&id); err != nil {
				return 0, 0, err
			}
			deliveryID, err := queuedDeliveryIntentID(tx, id)
			return id, deliveryID, err
		}
		return 0, 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	var deliveryID int64
	if recordIntent && options.SourceSessionID != "" {
		if deliveryID, err = insertQueuedDeliveryIntent(tx, options.SourceSessionID, sessionID, message, id, relayHops > 0); err != nil {
			return 0, 0, err
		}
	}
	return id, deliveryID, nil
}

func (r *Registry) readExists(query string, args ...any) (bool, error) {
	var exists bool
	err := r.read.QueryRow(query, args...).Scan(&exists)
	return exists, err
}

func (r *Registry) QueueCount(sessionID string) int {
	var n int
	r.read.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, sessionID).Scan(&n)
	return n
}

func (r *Registry) PendingSteer(sessionID string) (bool, error) {
	mode, err := r.busyMode()
	if err != nil {
		return false, err
	}
	var found int
	err = r.read.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight') AND `+queueSteerRow("message_queue", mode), sessionID).Scan(&found)
	return found > 0, err
}

func (r *Registry) QueueCounts() (map[string]int, error) {
	rows, err := r.read.Query(`SELECT session_id,COUNT(*) FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var sessionID string
		var count int
		if err := rows.Scan(&sessionID, &count); err != nil {
			return nil, err
		}
		counts[sessionID] = count
	}
	return counts, rows.Err()
}
