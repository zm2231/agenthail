package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type DeliveryIntentStatus string

const (
	DeliveryIntentSubmitted DeliveryIntentStatus = "submitted"
	DeliveryIntentSent      DeliveryIntentStatus = "sent"
	DeliveryIntentQueued    DeliveryIntentStatus = "queued"
	DeliveryIntentDelivered DeliveryIntentStatus = "delivered"
	DeliveryIntentUnknown   DeliveryIntentStatus = "unknown"
	DeliveryIntentFailed    DeliveryIntentStatus = "failed"
	DeliveryIntentExpired   DeliveryIntentStatus = "expired"
)

type DeliveryIntentInput struct {
	SenderSessionID string
	TargetSessionID string
	ProviderKey     string
	Message         string
	Status          DeliveryIntentStatus
	Evidence        surface.DeliveryEvidence
}

type DeliveryIntent struct {
	ID                  int64
	SenderSessionID     string
	TargetSessionID     string
	ProviderKey         string
	Message             string
	QueueID             int64
	Status              DeliveryIntentStatus
	Evidence            surface.DeliveryEvidence
	Failure             string
	NotificationQueueID int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (r *Registry) RecordDeliveryIntent(input DeliveryIntentInput) (*DeliveryIntent, error) {
	if err := input.validate(); err != nil {
		return nil, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if input.ProviderKey != "" {
		intent, found, err := deliveryIntentByProviderKey(tx, input.TargetSessionID, input.ProviderKey)
		if err != nil {
			return nil, err
		}
		if found {
			if intent.SenderSessionID != input.SenderSessionID {
				return nil, fmt.Errorf("provider delivery %q is already bound to another sender", input.ProviderKey)
			}
			return intent, tx.Commit()
		}
	}
	res, err := tx.Exec(`INSERT INTO delivery_intents(sender_session_id,target_session_id,provider_key,message,status,evidence) VALUES(?,?,?,?,?,?)`, input.SenderSessionID, input.TargetSessionID, input.ProviderKey, boundedIntentMessage(input.Message), input.Status, input.Evidence)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	intent, err := deliveryIntentByID(tx, id)
	if err != nil {
		return nil, err
	}
	return intent, tx.Commit()
}

func (input DeliveryIntentInput) validate() error {
	if strings.TrimSpace(input.SenderSessionID) == "" || strings.TrimSpace(input.TargetSessionID) == "" {
		return fmt.Errorf("delivery intent requires sender and target sessions")
	}
	if input.Status != DeliveryIntentSubmitted && input.Status != DeliveryIntentSent && input.Status != DeliveryIntentQueued && input.Status != DeliveryIntentUnknown {
		return fmt.Errorf("delivery intent cannot start as %q", input.Status)
	}
	if strings.TrimSpace(string(input.Evidence)) == "" {
		return fmt.Errorf("delivery intent evidence is required")
	}
	return nil
}

func (r *Registry) DeliveryIntent(id int64) (*DeliveryIntent, error) {
	return deliveryIntentByID(r.db, id)
}

func (r *Registry) ReconcileDeliveryIntent(targetSessionID, providerKey string) (bool, error) {
	if strings.TrimSpace(targetSessionID) == "" || strings.TrimSpace(providerKey) == "" {
		return false, fmt.Errorf("delivery reconciliation requires target and provider key")
	}
	res, err := r.db.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure='',updated_at=datetime('now') WHERE target_session_id=? AND provider_key=? AND status IN (?,?,?,?)`, DeliveryIntentDelivered, surface.EvidenceDelivered, targetSessionID, providerKey, DeliveryIntentSubmitted, DeliveryIntentSent, DeliveryIntentQueued, DeliveryIntentUnknown)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Registry) FailDeliveryIntent(id int64, status DeliveryIntentStatus, failure string) (bool, error) {
	if status != DeliveryIntentFailed && status != DeliveryIntentExpired {
		return false, fmt.Errorf("delivery failure status must be failed or expired")
	}
	if strings.TrimSpace(failure) == "" {
		return false, fmt.Errorf("delivery failure reason is required")
	}
	evidence := surface.EvidenceFailed
	if status == DeliveryIntentExpired {
		evidence = surface.EvidenceExpired
	}
	res, err := r.db.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure=?,updated_at=datetime('now') WHERE id=? AND status IN (?,?,?,?)`, status, evidence, failure, id, DeliveryIntentSubmitted, DeliveryIntentSent, DeliveryIntentQueued, DeliveryIntentUnknown)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Registry) BindDeliveryIntentQueue(id, queueID int64) error {
	_, err := r.db.Exec(`UPDATE delivery_intents SET queue_id=?,updated_at=datetime('now') WHERE id=?`, queueID, id)
	return err
}

func (r *Registry) FailQueuedDeliveryIntent(queueID int64, failure string) (int64, bool, error) {
	return r.finishQueuedDeliveryIntent(queueID, DeliveryIntentFailed, failure)
}

func (r *Registry) ExpireQueuedDeliveryIntent(queueID int64, failure string) (int64, bool, error) {
	return r.finishQueuedDeliveryIntent(queueID, DeliveryIntentExpired, failure)
}

func (r *Registry) finishQueuedDeliveryIntent(queueID int64, status DeliveryIntentStatus, failure string) (int64, bool, error) {
	var id int64
	err := r.db.QueryRow(`SELECT id FROM delivery_intents WHERE queue_id=?`, queueID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	changed, err := r.FailDeliveryIntent(id, status, failure)
	if err != nil || !changed {
		return id, false, err
	}
	_, queued, err := r.QueueDeliveryFailureNotice(id)
	return id, queued, err
}

func (r *Registry) QueueDeliveryFailureNotice(id int64) (int64, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	intent, err := deliveryIntentByID(tx, id)
	if err != nil {
		return 0, false, err
	}
	if intent.Status != DeliveryIntentFailed && intent.Status != DeliveryIntentExpired {
		return 0, false, nil
	}
	if intent.NotificationQueueID != 0 {
		return intent.NotificationQueueID, false, tx.Commit()
	}
	notice := fmt.Sprintf("[agenthail delivery failure id=%d target=%s] %s", intent.ID, intent.TargetSessionID, intent.Failure)
	key := fmt.Sprintf("delivery-intent-notice:%d", intent.ID)
	expiresAt := time.Now().Add(queueMessageTTL).UnixMilli()
	res, err := tx.Exec(`INSERT INTO message_queue(session_id,message,operation,delivery_key,expires_at_ms,status,updated_at) VALUES(?,?,? ,?,?,'pending',datetime('now'))`, intent.SenderSessionID, notice, QueueOperationMessage, key, expiresAt)
	if err != nil {
		return 0, false, err
	}
	queueID, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET notification_queue_id=?,updated_at=datetime('now') WHERE id=? AND notification_queue_id IS NULL`, queueID, intent.ID); err != nil {
		return 0, false, err
	}
	payload, err := json.Marshal(map[string]any{"deliveryId": intent.ID, "sessionId": intent.TargetSessionID, "sourceSessionId": intent.SenderSessionID, "message": boundedIntentMessage(intent.Message), "reason": intent.Failure, "at": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO catalog_events(dedupe_key,type,entity_id,payload,created_at) VALUES(?,?,?,?,datetime('now'))`, fmt.Sprintf("delivery.problem:%d", intent.ID), "delivery.problem", intent.TargetSessionID, payload); err != nil {
		return 0, false, err
	}
	return queueID, true, tx.Commit()
}

type deliveryIntentQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func deliveryIntentByID(q deliveryIntentQuerier, id int64) (*DeliveryIntent, error) {
	return scanDeliveryIntent(q.QueryRow(`SELECT id,sender_session_id,target_session_id,provider_key,message,queue_id,status,evidence,failure,COALESCE(notification_queue_id,0),created_at,updated_at FROM delivery_intents WHERE id=?`, id))
}

func deliveryIntentByProviderKey(q deliveryIntentQuerier, targetSessionID, providerKey string) (*DeliveryIntent, bool, error) {
	intent, err := scanDeliveryIntent(q.QueryRow(`SELECT id,sender_session_id,target_session_id,provider_key,message,queue_id,status,evidence,failure,COALESCE(notification_queue_id,0),created_at,updated_at FROM delivery_intents WHERE target_session_id=? AND provider_key=?`, targetSessionID, providerKey))
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	return intent, err == nil, err
}

func scanDeliveryIntent(row *sql.Row) (*DeliveryIntent, error) {
	var intent DeliveryIntent
	var status, evidence, createdAt, updatedAt string
	if err := row.Scan(&intent.ID, &intent.SenderSessionID, &intent.TargetSessionID, &intent.ProviderKey, &intent.Message, &intent.QueueID, &status, &evidence, &intent.Failure, &intent.NotificationQueueID, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	intent.Status = DeliveryIntentStatus(status)
	intent.Evidence = surface.DeliveryEvidence(evidence)
	var err error
	intent.CreatedAt, err = time.ParseInLocation("2006-01-02 15:04:05", createdAt, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("parse delivery intent creation timestamp: %w", err)
	}
	intent.UpdatedAt, err = time.ParseInLocation("2006-01-02 15:04:05", updatedAt, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("parse delivery intent update timestamp: %w", err)
	}
	return &intent, nil
}

func boundedIntentMessage(message string) string {
	const max = 16 * 1024
	if len(message) <= max {
		return message
	}
	return message[:max]
}
