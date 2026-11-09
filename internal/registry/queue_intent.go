package registry

import (
	"database/sql"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// queueIntent names the delivery intent a queued message carries: a new one
// from Sender, which is Agenthail itself when empty or not a session, or the
// submitted intent Bind.
type queueIntent struct {
	Sender string
	Bind   int64
}

func (q queueIntent) attach(tx *sql.Tx, targetSessionID, message string, queueID int64, relayed bool) (int64, error) {
	if q.Bind != 0 {
		_, err := tx.Exec(`UPDATE delivery_intents SET queue_id=?,status=?,evidence=?,updated_at=datetime('now') WHERE id=? AND status=?`, queueID, DeliveryIntentQueued, surface.EvidenceQueued, q.Bind, DeliveryIntentSubmitted)
		return q.Bind, err
	}
	sender, err := intentSenderTx(tx, q.Sender)
	if err != nil {
		return 0, err
	}
	return insertQueuedDeliveryIntent(tx, sender, targetSessionID, message, queueID, relayed)
}

func (q queueIntent) attachExisting(tx *sql.Tx, targetSessionID, message string, queueID int64, relayed bool) (int64, error) {
	existing, err := queuedDeliveryIntentID(tx, queueID)
	if err != nil {
		return 0, err
	}
	if existing == 0 {
		return q.attach(tx, targetSessionID, message, queueID, relayed)
	}
	if q.Bind != 0 {
		if _, err := tx.Exec(`DELETE FROM delivery_intents WHERE id=? AND status=?`, q.Bind, DeliveryIntentSubmitted); err != nil {
			return 0, err
		}
	}
	return existing, nil
}

func queueNoticeTx(tx *sql.Tx, sessionID, notice, key string) (int64, error) {
	queued, err := enqueueMessageTx(tx, time.Now(), sessionID, notice, key, surface.SendOptions{SourceSessionID: surface.SystemSessionID}, 0, QueueOperationMessage, queueIntent{})
	return queued.ID, err
}

func intentSenderTx(tx *sql.Tx, sender string) (string, error) {
	if sender == "" {
		sender = surface.SystemSessionID
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=?)`, sender).Scan(&exists); err != nil || exists {
		return sender, err
	}
	if session, ok := surface.AgenthailSender(sender, ""); ok {
		return sender, registerSessionRowTx(tx, session)
	}
	return intentSenderTx(tx, surface.SystemSessionID)
}
