package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

// sqlRandomUUID is a version 4 uuid in SQL, for intents recorded before
// intents carried one.
const sqlRandomUUID = `lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', 1 + abs(random()) % 4, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))`

type DeliveryIntentStatus string

const OperatorSessionID = "agenthail-operator"

func (r *Registry) EnsureOperatorSession() error {
	if _, err := r.Session(OperatorSessionID); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	return r.RegisterSession(surface.Session{ID: OperatorSessionID, Surface: surface.SurfaceKind("agenthail"), RuntimeKind: surface.RuntimeOperator, Name: "Agenthail operator"})
}

// EnsureUnrepliableSender records a session on a host this daemon has not
// paired, so a message it forwarded keeps its sender's name. Like the
// operator it is a sink: no catalog row, and a message to it is refused with
// the original sender's address, because there is no route back to that host.
func (r *Registry) EnsureUnrepliableSender(hostID, sessionID, name string) (string, error) {
	if !ValidHostID(hostID) || strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("an unrepliable sender needs its host and session ids")
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), "/"))
	if name == "" {
		name = hostID + "/" + sessionID
	}
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	sender := surface.UnrepliableSender(hostID, sessionID, name)
	return sender.ID, r.RegisterSession(sender)
}

const (
	DeliveryIntentSubmitted DeliveryIntentStatus = "submitted"
	DeliveryIntentSent      DeliveryIntentStatus = "sent"
	DeliveryIntentQueued    DeliveryIntentStatus = "queued"
	DeliveryIntentDelivered DeliveryIntentStatus = "delivered"
	DeliveryIntentUnknown   DeliveryIntentStatus = "unknown"
	DeliveryIntentFailed    DeliveryIntentStatus = "failed"
	// DeliveryIntentHeld is a message its target's runtime took and parked
	// under its own policy instead of passing it to the model.
	DeliveryIntentHeld DeliveryIntentStatus = "held"
)

type DeliveryIntentInput struct {
	// UUID is the message's provider identity when the caller already sent
	// under one; otherwise the intent gets a new one.
	UUID            string
	SenderSessionID string
	TargetSessionID string
	ProviderKey     string
	Message         string
	Status          DeliveryIntentStatus
	Evidence        surface.DeliveryEvidence
}

// DeliveryIntent is one message on its way to a session. UUID is the
// message's identity at the provider: a Claude session records it as the
// transcript record's uuid and runs a given uuid once, so a resend after an
// uncertain outcome cannot run the turn twice.
type DeliveryIntent struct {
	ID                  int64
	UUID                string
	SenderSessionID     string
	TargetSessionID     string
	ProviderKey         string
	Message             string
	QueueID             int64
	Relayed             bool
	Status              DeliveryIntentStatus
	Evidence            surface.DeliveryEvidence
	Failure             string
	NotificationQueueID int64
	// Retries counts explicit resends; each one is a new attempt whose
	// failure is reported on its own.
	Retries   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type DeliveryProblem struct {
	DeliveryID      int64  `json:"deliveryId"`
	SessionID       string `json:"sessionId"`
	SourceSessionID string `json:"sourceSessionId"`
	// AttentionSessionID is the session that needs a person because of this
	// problem: the agent that sent the message, until it has been told.
	AttentionSessionID string               `json:"attentionSessionId,omitempty"`
	Message            string               `json:"message"`
	Reason             string               `json:"reason"`
	Status             DeliveryIntentStatus `json:"status"`
	At                 time.Time            `json:"at"`
}

func (r *Registry) ListDeliveryProblems() ([]DeliveryProblem, error) {
	rows, err := r.read.Query(`
		SELECT di.id,di.target_session_id,di.sender_session_id,di.message,di.failure,di.status,di.updated_at,COALESCE(mq.status,'')='delivered'
		FROM delivery_intents di LEFT JOIN message_queue mq ON mq.id=di.notification_queue_id
		WHERE di.status IN (?,?) AND COALESCE(di.dismissed_at,'')=''
		ORDER BY di.updated_at DESC, di.id DESC
		LIMIT 50`, DeliveryIntentFailed, DeliveryIntentHeld)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	problems := make([]DeliveryProblem, 0, 50)
	for rows.Next() {
		var problem DeliveryProblem
		var status, updatedAt string
		var notified bool
		if err := rows.Scan(&problem.DeliveryID, &problem.SessionID, &problem.SourceSessionID, &problem.Message, &problem.Reason, &status, &updatedAt, &notified); err != nil {
			return nil, err
		}
		problem.AttentionSessionID = deliveryAttentionSession(problem.SourceSessionID, notified)
		problem.Message = boundedIntentMessage(problem.Message)
		problem.Status = DeliveryIntentStatus(status)
		problem.At, err = time.ParseInLocation("2006-01-02 15:04:05", updatedAt, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("parse delivery problem timestamp: %w", err)
		}
		problems = append(problems, problem)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return problems, nil
}

// deliveryAttentionSession flags the sender of a failed message, never its
// target. A person's own send has no session to flag; it stays in the problem
// list. An agent stops being flagged once its failure notice reached it.
func deliveryAttentionSession(senderSessionID string, notified bool) string {
	if senderSessionID == OperatorSessionID || surface.IsUnrepliableSender(senderSessionID) || notified {
		return ""
	}
	return senderSessionID
}

func (r *Registry) DismissDeliveryProblem(id int64) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var targetSessionID, status string
	var retries int
	var dismissedAt sql.NullString
	if err := tx.QueryRow(`SELECT target_session_id,status,retries,dismissed_at FROM delivery_intents WHERE id=?`, id).Scan(&targetSessionID, &status, &retries, &dismissedAt); err != nil {
		if err == sql.ErrNoRows {
			return false, sql.ErrNoRows
		}
		return false, err
	}
	if status != string(DeliveryIntentFailed) && status != string(DeliveryIntentHeld) {
		return false, fmt.Errorf("delivery %d is not a failure", id)
	}
	if dismissedAt.Valid && dismissedAt.String != "" {
		return false, tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET dismissed_at=datetime('now') WHERE id=? AND status=? AND COALESCE(dismissed_at,'')=''`, id, status); err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]any{"deliveryId": id})
	if err != nil {
		return false, err
	}
	if _, _, err := appendCatalogEventTx(tx, CatalogEvent{
		DedupeKey: fmt.Sprintf("delivery.dismissed:%d:%d", id, retries),
		Type:      "delivery.dismissed",
		EntityID:  targetSessionID,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return false, err
	}
	return true, tx.Commit()
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
	id := input.UUID
	if id == "" {
		id = uuid.NewString()
	}
	res, err := tx.Exec(`INSERT INTO delivery_intents(uuid,sender_session_id,target_session_id,provider_key,message,status,evidence) VALUES(?,?,?,?,?,?,?)`, id, input.SenderSessionID, input.TargetSessionID, input.ProviderKey, boundedIntentMessage(input.Message), input.Status, input.Evidence)
	if err != nil {
		return nil, err
	}
	row, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	intent, err := deliveryIntentByID(tx, row)
	if err != nil {
		return nil, err
	}
	return intent, tx.Commit()
}

// RecordSessionCreationIntent records a new session's first message under the
// uuid it was sent with, so the intent names the turn it may have started.
func (r *Registry) RecordSessionCreationIntent(sessionID, messageUUID, message string) (*DeliveryIntent, error) {
	if err := r.EnsureOperatorSession(); err != nil {
		return nil, err
	}
	return r.RecordDeliveryIntent(DeliveryIntentInput{
		UUID:            messageUUID,
		SenderSessionID: OperatorSessionID,
		TargetSessionID: sessionID,
		Message:         message,
		Status:          DeliveryIntentSubmitted,
		Evidence:        surface.EvidenceSubmitted,
	})
}

func (input DeliveryIntentInput) validate() error {
	if strings.TrimSpace(input.SenderSessionID) == "" || strings.TrimSpace(input.TargetSessionID) == "" {
		return fmt.Errorf("delivery intent requires sender and target sessions")
	}
	if input.UUID != "" {
		if _, err := uuid.Parse(input.UUID); err != nil {
			return fmt.Errorf("delivery intent uuid: %w", err)
		}
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

// DeliveryIntentUUIDForQueue returns the uuid of the intent a queued message
// belongs to, or "" when it has none.
func (r *Registry) DeliveryIntentUUIDForQueue(queueID int64) (string, error) {
	var id string
	err := r.db.QueryRow(`SELECT uuid FROM delivery_intents WHERE queue_id=? ORDER BY id DESC LIMIT 1`, queueID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func (r *Registry) ReconcileDeliveryIntent(targetSessionID, providerKey string) (bool, error) {
	if strings.TrimSpace(targetSessionID) == "" || strings.TrimSpace(providerKey) == "" {
		return false, fmt.Errorf("delivery reconciliation requires target and provider key")
	}
	res, err := r.db.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure='',updated_at=datetime('now') WHERE target_session_id=? AND provider_key=? AND status IN (?,?,?,?,?)`, DeliveryIntentDelivered, surface.EvidenceDelivered, targetSessionID, providerKey, DeliveryIntentSubmitted, DeliveryIntentSent, DeliveryIntentQueued, DeliveryIntentUnknown, DeliveryIntentHeld)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Registry) FailDeliveryIntent(id int64, failure string) (bool, error) {
	return failDeliveryIntent(r.db, id, failure)
}

// MarkDeliveryIntentSent completes the same logical intent created before a
// provider side effect. Keeping this transition on the original row preserves
// the delivery ID across accepted, submitted, and reconciled outcomes. A hold
// the target reported before the send was recorded stands.
func (r *Registry) MarkDeliveryIntentSent(id int64, providerKey string, evidence surface.DeliveryEvidence) (bool, error) {
	if id == 0 || strings.TrimSpace(string(evidence)) == "" {
		return false, fmt.Errorf("delivery intent completion requires an id and evidence")
	}
	res, err := r.db.Exec(`UPDATE delivery_intents SET status=CASE WHEN status=? THEN status ELSE ? END,evidence=?,provider_key=CASE WHEN ?!='' THEN ? ELSE provider_key END,failure=CASE WHEN status=? THEN failure ELSE '' END,updated_at=datetime('now') WHERE id=? AND status IN (?,?)`, DeliveryIntentHeld, DeliveryIntentSent, evidence, providerKey, providerKey, DeliveryIntentHeld, id, DeliveryIntentSubmitted, DeliveryIntentHeld)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DiscardDeliveryIntent drops a submitted intent refused before any provider
// effect; the caller already holds the error, so it is not a durable problem.
func (r *Registry) DiscardDeliveryIntent(id int64) error {
	_, err := r.db.Exec(`DELETE FROM delivery_intents WHERE id=? AND status=?`, id, DeliveryIntentSubmitted)
	return err
}

// QueueDeliveryUsingIntent moves a pre-effect submitted intent into the
// durable queue without creating a second delivery identity.
func (r *Registry) QueueDeliveryUsingIntent(intentID int64, message, deliveryKey string, options surface.SendOptions) (int64, int64, error) {
	if intentID == 0 {
		return 0, 0, fmt.Errorf("queued delivery requires an intent")
	}
	if options.BusyDelivery == "steer" && options.HoldsForQueue() {
		options.BusyDelivery = "queue"
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var senderSessionID, targetSessionID string
	var status string
	if err := tx.QueryRow(`SELECT sender_session_id,target_session_id,status FROM delivery_intents WHERE id=?`, intentID).Scan(&senderSessionID, &targetSessionID, &status); err != nil {
		return 0, 0, err
	}
	if status != string(DeliveryIntentSubmitted) {
		return 0, 0, fmt.Errorf("delivery intent %d is not submitted", intentID)
	}
	res, err := tx.Exec(`INSERT INTO message_queue (session_id,message,operation,delivery_key,model,source_session_id,turn_options,relay_hops,busy_delivery,author,uploads,status,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,'pending',datetime('now'))`, targetSessionID, message, QueueOperationMessage, deliveryKey, options.Model, senderSessionID, options.TurnOptions, 0, options.BusyDelivery, options.Author, options.Uploads)
	if err != nil {
		if deliveryKey == "" || !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, 0, err
		}
		var queueID, existingIntentID int64
		if scanErr := tx.QueryRow(`SELECT id FROM message_queue WHERE delivery_key=?`, deliveryKey).Scan(&queueID); scanErr != nil {
			return 0, 0, scanErr
		}
		scanErr := tx.QueryRow(`SELECT id FROM delivery_intents WHERE queue_id=?`, queueID).Scan(&existingIntentID)
		if scanErr == sql.ErrNoRows {
			if _, bindErr := tx.Exec(`UPDATE delivery_intents SET queue_id=?,status=?,evidence=?,updated_at=datetime('now') WHERE id=? AND status=?`, queueID, DeliveryIntentQueued, surface.EvidenceQueued, intentID, DeliveryIntentSubmitted); bindErr != nil {
				return 0, 0, bindErr
			}
			existingIntentID = intentID
		} else if scanErr != nil {
			return 0, 0, scanErr
		} else if _, deleteErr := tx.Exec(`DELETE FROM delivery_intents WHERE id=? AND status=?`, intentID, DeliveryIntentSubmitted); deleteErr != nil {
			return 0, 0, deleteErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return 0, 0, commitErr
		}
		return queueID, existingIntentID, nil
	}
	queueID, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET queue_id=?,status=?,evidence=?,updated_at=datetime('now') WHERE id=? AND status=?`, queueID, DeliveryIntentQueued, surface.EvidenceQueued, intentID, DeliveryIntentSubmitted); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: targetSessionID, SourceSessionID: senderSessionID, QueueID: queueID, Message: message})
	return queueID, intentID, nil
}

// FailDeliveryIntentWithNotice makes the failure durable before returning. If
// notice insertion fails, the failed intent still commits and can be retried
// by QueueDeliveryFailureNotice; it is never silently reverted to submitted.
func (r *Registry) FailDeliveryIntentWithNotice(id int64, failure string) (bool, error) {
	if id == 0 {
		return false, fmt.Errorf("delivery failure requires an intent")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	changed, err := failDeliveryIntent(tx, id, failure)
	if err != nil || !changed {
		_ = tx.Rollback()
		return changed, err
	}
	_, _, noticeErr := queueDeliveryFailureNotice(tx, id)
	commitErr := tx.Commit()
	if commitErr != nil {
		return false, commitErr
	}
	return true, noticeErr
}

type deliveryIntentExecutor interface {
	deliveryIntentQuerier
	Exec(query string, args ...any) (sql.Result, error)
}

func failDeliveryIntent(q deliveryIntentExecutor, id int64, failure string) (bool, error) {
	if strings.TrimSpace(failure) == "" {
		return false, fmt.Errorf("delivery failure reason is required")
	}
	res, err := q.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure=?,updated_at=datetime('now') WHERE id=? AND status IN (?,?,?,?,?)`, DeliveryIntentFailed, surface.EvidenceFailed, failure, id, DeliveryIntentSubmitted, DeliveryIntentSent, DeliveryIntentQueued, DeliveryIntentUnknown, DeliveryIntentHeld)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func insertQueuedDeliveryIntent(tx *sql.Tx, senderSessionID, targetSessionID, message string, queueID int64, relayed bool) (int64, error) {
	res, err := tx.Exec(`INSERT INTO delivery_intents(uuid,sender_session_id,target_session_id,message,queue_id,relayed,status,evidence) SELECT ?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM sessions WHERE id=?) AND EXISTS(SELECT 1 FROM sessions WHERE id=?)`, uuid.NewString(), senderSessionID, targetSessionID, boundedIntentMessage(message), queueID, relayed, DeliveryIntentQueued, surface.EvidenceQueued, senderSessionID, targetSessionID)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return 0, err
	}
	return res.LastInsertId()
}

func queuedDeliveryIntentID(q deliveryIntentQuerier, queueID int64) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM delivery_intents WHERE queue_id=?`, queueID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func markQueuedDeliveryIntent(tx *sql.Tx, queueID int64, status DeliveryIntentStatus, evidence surface.DeliveryEvidence, providerKey string) error {
	_, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,provider_key=CASE WHEN ?!='' THEN ? ELSE provider_key END,updated_at=datetime('now') WHERE queue_id=? AND status IN (?,?)`, status, evidence, providerKey, providerKey, queueID, DeliveryIntentQueued, DeliveryIntentUnknown)
	return err
}

// retryQueuedDeliveryIntent returns the intent of a resent queue row to
// queued. A failure still on the problem list is withdrawn.
func retryQueuedDeliveryIntent(tx *sql.Tx, queueID int64) error {
	var id int64
	var targetSessionID, status string
	var retries int
	var dismissedAt sql.NullString
	err := tx.QueryRow(`SELECT id,target_session_id,status,retries,dismissed_at FROM delivery_intents WHERE queue_id=? AND status IN (?,?)`, queueID, DeliveryIntentFailed, DeliveryIntentUnknown).Scan(&id, &targetSessionID, &status, &retries, &dismissedAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure='',dismissed_at=NULL,notification_queue_id=NULL,retries=retries+1,updated_at=datetime('now') WHERE id=?`, DeliveryIntentQueued, surface.EvidenceQueued, id); err != nil {
		return err
	}
	if status != string(DeliveryIntentFailed) || dismissedAt.Valid && dismissedAt.String != "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"deliveryId": id})
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("delivery.retried:%d:%d", id, retries), Type: "delivery.dismissed", EntityID: targetSessionID, Payload: payload, CreatedAt: time.Now().UTC()})
	return err
}

func finishQueuedDeliveryIntent(tx *sql.Tx, queueID int64, failure string) (bool, error) {
	id, err := queuedDeliveryIntentID(tx, queueID)
	if err != nil || id == 0 {
		return false, err
	}
	changed, err := failDeliveryIntent(tx, id, failure)
	if err != nil || !changed {
		return false, err
	}
	_, queued, err := queueDeliveryFailureNotice(tx, id)
	return queued, err
}

func (r *Registry) QueueDeliveryFailureNotice(id int64) (int64, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	queueID, queued, err := queueDeliveryFailureNotice(tx, id)
	if err != nil {
		return 0, false, err
	}
	return queueID, queued, tx.Commit()
}

func (r *Registry) RecordReplyForwardFailure(senderSessionID, targetSessionID, providerKey, message, failure string) (bool, error) {
	if strings.TrimSpace(senderSessionID) == "" || strings.TrimSpace(targetSessionID) == "" || strings.TrimSpace(providerKey) == "" {
		return false, fmt.Errorf("reply-forward failure requires sender, target, and provider key")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, found, err := deliveryIntentByProviderKey(tx, targetSessionID, providerKey); err != nil {
		return false, err
	} else if found {
		return false, tx.Commit()
	}
	result, err := tx.Exec(`INSERT INTO delivery_intents(uuid,sender_session_id,target_session_id,provider_key,message,relayed,status,evidence) VALUES(?,?,?,?,?,1,?,?)`, uuid.NewString(), senderSessionID, targetSessionID, providerKey, boundedIntentMessage(message), DeliveryIntentSubmitted, surface.EvidenceSubmitted)
	if err != nil {
		return false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return false, err
	}
	if changed, err := failDeliveryIntent(tx, id, failure); err != nil || !changed {
		return false, err
	}
	_, queued, err := queueDeliveryFailureNotice(tx, id)
	if err != nil {
		return false, err
	}
	return queued, tx.Commit()
}

func queueDeliveryFailureNotice(tx *sql.Tx, id int64) (int64, bool, error) {
	intent, err := deliveryIntentByID(tx, id)
	if err != nil {
		return 0, false, err
	}
	if intent.Status != DeliveryIntentFailed {
		return 0, false, nil
	}
	if intent.NotificationQueueID != 0 {
		return intent.NotificationQueueID, false, nil
	}
	if intent.Relayed || surface.IsUnrepliableSender(intent.SenderSessionID) {
		// A relay source never sent this message: a notice would start a turn
		// whose completion the same relay forwards again. An unrepliable
		// sender has no route back, so nothing would read it. Either way the
		// problem is listed.
		appended, err := appendDeliveryProblemEvent(tx, intent)
		return 0, appended, err
	}
	notice := fmt.Sprintf("[agenthail delivery failure id=%d target=%s] %s", intent.ID, intent.TargetSessionID, intent.Failure)
	key := fmt.Sprintf("delivery-intent-notice:%d:%d", intent.ID, intent.Retries)
	res, err := tx.Exec(`INSERT INTO message_queue(session_id,message,operation,delivery_key,status,updated_at) VALUES(?,?,?,?,'pending',datetime('now'))`, intent.SenderSessionID, notice, QueueOperationMessage, key)
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
	if _, err := appendDeliveryProblemEvent(tx, intent); err != nil {
		return 0, false, err
	}
	return queueID, true, nil
}

// queueDeliveryRefusalNotice tells the agent that sent a queued message that
// its target refused to start and why. The message stays queued.
func queueDeliveryRefusalNotice(tx *sql.Tx, queueID, refusal int64, reason string) error {
	id, err := queuedDeliveryIntentID(tx, queueID)
	if err != nil || id == 0 {
		return err
	}
	intent, err := deliveryIntentByID(tx, id)
	if err != nil {
		return err
	}
	if intent.Relayed || intent.SenderSessionID == "" || surface.IsUnrepliableSender(intent.SenderSessionID) {
		return nil
	}
	notice := fmt.Sprintf("[agenthail delivery held id=%d target=%s] %s. The message stays queued and is delivered once the target starts.", intent.ID, intent.TargetSessionID, reason)
	key := fmt.Sprintf("delivery-intent-refusal:%d:%d", intent.ID, refusal)
	_, err = tx.Exec(`INSERT INTO message_queue(session_id,message,operation,delivery_key,status,updated_at) VALUES(?,?,?,?,'pending',datetime('now')) ON CONFLICT DO NOTHING`, intent.SenderSessionID, notice, QueueOperationMessage, key)
	return err
}

func appendDeliveryProblemEvent(tx *sql.Tx, intent *DeliveryIntent) (bool, error) {
	at := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"deliveryId": intent.ID, "sessionId": intent.TargetSessionID, "sourceSessionId": intent.SenderSessionID, "attentionSessionId": deliveryAttentionSession(intent.SenderSessionID, false), "message": boundedIntentMessage(intent.Message), "reason": intent.Failure, "status": intent.Status, "at": at.Format(time.RFC3339Nano)})
	if err != nil {
		return false, err
	}
	_, appended, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("delivery.%s:%d:%d", intent.Status, intent.ID, intent.Retries), Type: "delivery.problem", EntityID: intent.TargetSessionID, Payload: payload, CreatedAt: at})
	return appended, err
}

type deliveryIntentQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func deliveryIntentByID(q deliveryIntentQuerier, id int64) (*DeliveryIntent, error) {
	return scanDeliveryIntent(q.QueryRow(`SELECT id,uuid,sender_session_id,target_session_id,provider_key,message,queue_id,relayed,status,evidence,failure,COALESCE(notification_queue_id,0),retries,created_at,updated_at FROM delivery_intents WHERE id=?`, id))
}

func deliveryIntentByProviderKey(q deliveryIntentQuerier, targetSessionID, providerKey string) (*DeliveryIntent, bool, error) {
	intent, err := scanDeliveryIntent(q.QueryRow(`SELECT id,uuid,sender_session_id,target_session_id,provider_key,message,queue_id,relayed,status,evidence,failure,COALESCE(notification_queue_id,0),retries,created_at,updated_at FROM delivery_intents WHERE target_session_id=? AND provider_key=?`, targetSessionID, providerKey))
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	return intent, err == nil, err
}

func scanDeliveryIntent(row *sql.Row) (*DeliveryIntent, error) {
	var intent DeliveryIntent
	var status, evidence, createdAt, updatedAt string
	if err := row.Scan(&intent.ID, &intent.UUID, &intent.SenderSessionID, &intent.TargetSessionID, &intent.ProviderKey, &intent.Message, &intent.QueueID, &intent.Relayed, &status, &evidence, &intent.Failure, &intent.NotificationQueueID, &intent.Retries, &createdAt, &updatedAt); err != nil {
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
