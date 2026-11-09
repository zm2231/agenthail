package registry

import (
	"database/sql"
	"errors"
	"fmt"
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
	return r.enqueueMessage(sessionID, message, deliveryKey, options, 0, QueueOperationMessage, queueIntent{Sender: options.Sender()})
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
	id, created, err := queueCompactTx(tx, sessionID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if created {
		_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: id, Message: "compact"})
	}
	return id, nil
}

func queueCompactTx(tx *sql.Tx, sessionID string) (int64, bool, error) {
	var existing int64
	err := tx.QueryRow(`SELECT id FROM message_queue WHERE session_id=? AND operation=? AND status IN ('pending','inflight') ORDER BY id LIMIT 1`, sessionID, QueueOperationCompact).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,status,updated_at) VALUES (?,?,?,'pending',datetime('now'))`, sessionID, "/compact", QueueOperationCompact)
	if err != nil {
		return 0, false, err
	}
	id, err := res.LastInsertId()
	return id, true, err
}

func (r *Registry) QueueRelayMessage(sessionID, message, deliveryKey string, relayHops int) (int64, error) {
	return r.QueueRelayMessageWithOptions(sessionID, message, deliveryKey, relayHops, surface.SendOptions{})
}

func (r *Registry) QueueRelayMessageWithOptions(sessionID, message, deliveryKey string, relayHops int, options surface.SendOptions) (int64, error) {
	id, _, err := r.enqueueMessage(sessionID, message, deliveryKey, options, relayHops, QueueOperationMessage, queueIntent{Sender: options.Sender()})
	return id, err
}

func (r *Registry) queueMessageWithOptions(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation) (int64, error) {
	id, _, err := r.enqueueMessage(sessionID, message, deliveryKey, options, relayHops, operation, queueIntent{})
	return id, err
}

func (r *Registry) enqueueMessage(sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, intent queueIntent) (int64, int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	id, deliveryID, err := enqueueMessageTx(tx, r.now(), sessionID, message, deliveryKey, options, relayHops, operation, intent)
	if err != nil {
		return 0, 0, err
	}
	held, err := r.holdForOwnerTx(tx, id, sessionID, message, deliveryKey, options, relayHops, operation)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	if held {
		r.signalChange(ChangeHostDeliveries)
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, SourceSessionID: options.SourceSessionID, QueueID: id, Message: message, Result: options.Model})
	return id, deliveryID, nil
}

func enqueueMessageTx(tx *sql.Tx, now time.Time, sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, intent queueIntent) (int64, int64, error) {
	if options.BusyDelivery == "steer" && !options.QueuesAsSteer() {
		options.BusyDelivery = "queue"
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,delivery_key,model,source_session_id,turn_options,relay_hops,busy_delivery,author,uploads,person_name,status,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'pending',datetime('now'))`, sessionID, message, operation, deliveryKey, options.Model, options.SourceSessionID, options.TurnOptions, relayHops, options.BusyDelivery, options.Author, options.Uploads, options.PersonName)
	if err != nil {
		if deliveryKey == "" || !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, 0, err
		}
		var id int64
		if err := tx.QueryRow(`SELECT id FROM message_queue WHERE delivery_key=? AND delivery_key!=''`, deliveryKey).Scan(&id); err != nil {
			return 0, 0, err
		}
		if operation != QueueOperationMessage {
			return id, 0, nil
		}
		deliveryID, err := intent.attachExisting(tx, sessionID, message, id, relayHops > 0)
		return id, deliveryID, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	if operation != QueueOperationMessage {
		return id, 0, nil
	}
	deliveryID, err := intent.attach(tx, sessionID, message, id, relayHops > 0)
	if err != nil {
		return 0, 0, err
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

func (r *Registry) QueuedFromSender(sessionID, senderID string) (bool, error) {
	var found int
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM message_queue WHERE session_id=? AND source_session_id=? AND status IN ('pending','inflight'))`, sessionID, senderID).Scan(&found)
	return found == 1, err
}

func (r *Registry) PendingSteer(sessionID string) (bool, error) {
	var found int
	err := r.read.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight') AND `+queueSteerRow("message_queue", r.busyMode()), sessionID).Scan(&found)
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

// holdForOwnerTx makes a message queued for a session another host now owns,
// one that moved while the send waited for it, a send held for that host, as
// the move made the messages already queued for it.
func (r *Registry) holdForOwnerTx(tx *sql.Tx, id int64, sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation) (bool, error) {
	if operation != QueueOperationMessage || len(options.Uploads) > 0 {
		return false, nil
	}
	var owner string
	err := tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	key := deliveryKey
	if key == "" {
		key = fmt.Sprintf("held-%s-%d", sessionID, id)
	}
	request, err := r.hostSendRequest(HostSend{SessionID: sessionID, Message: message, Key: key, SourceSessionID: options.SourceSessionID, BusyDelivery: options.BusyDelivery, RelayHops: relayHops})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`UPDATE message_queue SET host_request=?,delivery_key=?,evidence=?,updated_at=datetime('now') WHERE id=?`, request, key, surface.EvidenceQueued, id)
	return err == nil, err
}
