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
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := refuseAfterMoveTx(tx, sessionID); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,status,updated_at) VALUES (?,?,?,'pending',datetime('now'))`, sessionID, command, QueueOperationGoal)
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
	if err := refuseAfterMoveTx(tx, sessionID); err != nil {
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
	queued, err := enqueueMessageTx(tx, r.now(), sessionID, message, deliveryKey, options, relayHops, operation, intent)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	r.signalHeld(queued)
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, SourceSessionID: options.SourceSessionID, QueueID: queued.ID, Message: message, Result: options.Model})
	return queued.ID, queued.DeliveryID, nil
}

// enqueued is a queue row and its delivery intent. Held means the row went
// to its session's host as a held send, which the caller signals once its
// transaction commits.
type enqueued struct {
	ID         int64
	DeliveryID int64
	Held       bool
}

func (r *Registry) signalHeld(queued enqueued) {
	if queued.Held {
		r.signalChange(ChangeHostDeliveries)
	}
}

func enqueueMessageTx(tx *sql.Tx, now time.Time, sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation, intent queueIntent) (enqueued, error) {
	if options.BusyDelivery == "steer" && !options.QueuesAsSteer() {
		options.BusyDelivery = "queue"
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,delivery_key,model,source_session_id,turn_options,relay_hops,busy_delivery,author,uploads,person_name,status,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'pending',datetime('now'))`, sessionID, message, operation, deliveryKey, options.Model, options.SourceSessionID, options.TurnOptions, relayHops, options.BusyDelivery, options.Author, options.Uploads, options.PersonName)
	if err != nil {
		if deliveryKey == "" || !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return enqueued{}, err
		}
		var id int64
		if err := tx.QueryRow(`SELECT id FROM message_queue WHERE delivery_key=? AND delivery_key!=''`, deliveryKey).Scan(&id); err != nil {
			return enqueued{}, err
		}
		if operation != QueueOperationMessage {
			return enqueued{ID: id}, nil
		}
		deliveryID, err := intent.attachExisting(tx, sessionID, message, id, relayHops > 0)
		return enqueued{ID: id, DeliveryID: deliveryID}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return enqueued{}, err
	}
	queued := enqueued{ID: id}
	if operation == QueueOperationMessage {
		if queued.DeliveryID, err = intent.attach(tx, sessionID, message, id, relayHops > 0); err != nil {
			return enqueued{}, err
		}
	}
	queued.Held, err = holdForOwnerTx(tx, id, sessionID, message, deliveryKey, options, relayHops, operation)
	if err != nil {
		return enqueued{}, err
	}
	return queued, nil
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

// ErrQueuedAfterMove refuses a queued item a paired host now owns the session
// for and cannot take as a held send: an operation, or a message with files,
// as the move fails the ones already queued.
var ErrQueuedAfterMove = errors.New("the session moved to another host before this was queued, and only a text message follows it there; send it again there")

// holdForOwnerTx makes a message queued for a session another host owns,
// one that moved while the send waited for it, a send held for that host, as
// the move made the messages already queued for it.
func holdForOwnerTx(tx *sql.Tx, id int64, sessionID, message, deliveryKey string, options surface.SendOptions, relayHops int, operation QueueOperation) (bool, error) {
	owner, err := sessionOwnerTx(tx, sessionID)
	if err != nil || owner == "" {
		return false, err
	}
	if operation != QueueOperationMessage || len(options.Uploads) > 0 {
		return false, ErrQueuedAfterMove
	}
	key := deliveryKey
	if key == "" {
		key = fmt.Sprintf("held-%s-%d", sessionID, id)
	}
	request, err := hostSendRequest(tx, HostSend{SessionID: sessionID, Message: message, Key: key, SourceSessionID: options.SourceSessionID, BusyDelivery: options.BusyDelivery, RelayHops: relayHops})
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`UPDATE message_queue SET host_request=?,delivery_key=?,evidence=?,updated_at=datetime('now') WHERE id=?`, request, key, surface.EvidenceQueued, id)
	return err == nil, err
}

func sessionOwnerTx(tx *sql.Tx, sessionID string) (string, error) {
	var owner string
	err := tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
}

func refuseAfterMoveTx(tx *sql.Tx, sessionID string) error {
	owner, err := sessionOwnerTx(tx, sessionID)
	if err == nil && owner != "" {
		return ErrQueuedAfterMove
	}
	return err
}
