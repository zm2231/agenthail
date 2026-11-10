package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

// sqlRandomUUID is a version 4 uuid in SQL, for intents recorded before
// intents carried one.
const sqlRandomUUID = `lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', 1 + abs(random()) % 4, 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))`

type DeliveryIntentStatus string

// readsNotices reports whether a sender has a session that reads a delivery
// notice. The person reads the target session itself, and the Agenthail CLI
// and an unrepliable sender have no session, so a notice queued to them would
// stay pending forever.
func readsNotices(senderSessionID string) bool {
	if _, ok := surface.AgenthailSender(senderSessionID, ""); ok {
		return false
	}
	return senderSessionID != "" && !surface.IsUnrepliableSender(senderSessionID)
}

// EnsureSender records the person or the Agenthail CLI the first time either
// sends, and does nothing for any other sender.
func (r *Registry) EnsureSender(id string) error {
	sender, ok := surface.AgenthailSender(id, "")
	if !ok {
		return nil
	}
	if _, err := r.Session(id); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	return r.RegisterSession(sender)
}

// PersonName is what sessions call the person: the name set with agenthail
// person name, else the full name of the account Agenthail runs under, else
// its login.
func (r *Registry) PersonName() (string, error) {
	return personName(r.db)
}

func personName(q catalogQuerier) (string, error) {
	name, err := personNameSetting(q)
	if err != nil || name != "" {
		return name, err
	}
	return accountName(), nil
}

// PersonNameSetting is the name set with agenthail person name, empty when
// none is.
func (r *Registry) PersonNameSetting() (string, error) {
	return personNameSetting(r.db)
}

func personNameSetting(q catalogQuerier) (string, error) {
	var name string
	err := q.QueryRow(`SELECT name FROM sessions WHERE id=?`, surface.PersonSessionID).Scan(&name)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return strings.TrimSpace(name), nil
}

// SetPersonName sets the name sessions call the person; an empty name goes
// back to the account's.
func (r *Registry) SetPersonName(name string) error {
	if err := r.EnsureSender(surface.PersonSessionID); err != nil {
		return err
	}
	_, err := r.db.Exec(`UPDATE sessions SET name=?, updated_at=datetime('now') WHERE id=?`, strings.TrimSpace(name), surface.PersonSessionID)
	return err
}

func accountName() string {
	account, err := user.Current()
	if err != nil {
		return os.Getenv("USER")
	}
	if name, _, _ := strings.Cut(account.Name, ","); strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return account.Username
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
	ID              int64
	UUID            string
	SenderSessionID string
	TargetSessionID string
	ProviderKey     string
	Message         string
	QueueID         int64
	Relayed         bool
	Status          DeliveryIntentStatus
	Evidence        surface.DeliveryEvidence
	Failure         string
	// Retries counts explicit resends; each one is a new attempt whose
	// failure is reported on its own.
	Retries   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// deletableFailure is a failure recorded before the target took the message:
// a send refused while its intent was only submitted, or a queue row that
// failed before any attempt left it unconfirmed.
const deletableFailure = `di.status='failed' AND di.failed_from IN ('submitted','queued')`

// settledDeliveryFailure matches a failure that no longer needs anyone: a
// later message from the same sender reached the same target, or it is more
// than a day old. A held message stays until its target releases or drops it.
const settledDeliveryFailure = `di.status='failed' AND (di.updated_at<datetime('now','-1 day') OR EXISTS (
	SELECT 1 FROM delivery_intents later
	WHERE later.sender_session_id=di.sender_session_id AND later.target_session_id=di.target_session_id
		AND later.id>di.id AND later.status IN ('sent','delivered')))`

var ErrDeliveryNotDeletable = errors.New("only a message that never reached its session can be deleted")

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
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return nil, err
	}
	return intent, tx.Commit()
}

// RecordSessionCreationIntent records a new session's first message from its
// creator under the uuid it was sent with, so the intent names the turn it may
// have started.
func (r *Registry) RecordSessionCreationIntent(sessionID, creatorID, messageUUID, message string) (*DeliveryIntent, error) {
	if err := r.EnsureSender(creatorID); err != nil {
		return nil, err
	}
	return r.RecordDeliveryIntent(DeliveryIntentInput{
		UUID:            messageUUID,
		SenderSessionID: creatorID,
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
	reconciled := false
	err := r.deliveryWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure='',updated_at=datetime('now') WHERE target_session_id=? AND provider_key=? AND provider_key!='' AND status IN (?,?,?,?,?)`, DeliveryIntentDelivered, surface.EvidenceDelivered, targetSessionID, providerKey, DeliveryIntentSubmitted, DeliveryIntentSent, DeliveryIntentQueued, DeliveryIntentUnknown, DeliveryIntentHeld)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		reconciled = n == 1
		return err
	})
	return reconciled, err
}

// MarkDeliveryIntentSent completes the same logical intent created before a
// provider side effect. Keeping this transition on the original row preserves
// the delivery ID across accepted, submitted, and reconciled outcomes. A hold
// the target reported before the send was recorded stands.
func (r *Registry) MarkDeliveryIntentSent(id int64, providerKey string, evidence surface.DeliveryEvidence) (bool, error) {
	if id == 0 || strings.TrimSpace(string(evidence)) == "" {
		return false, fmt.Errorf("delivery intent completion requires an id and evidence")
	}
	marked := false
	err := r.deliveryWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE delivery_intents SET status=CASE WHEN status=? THEN status ELSE ? END,evidence=?,provider_key=CASE WHEN ?!='' THEN ? ELSE provider_key END,failure=CASE WHEN status=? THEN failure ELSE '' END,updated_at=datetime('now') WHERE id=? AND status IN (?,?)`, DeliveryIntentHeld, DeliveryIntentSent, evidence, providerKey, providerKey, DeliveryIntentHeld, id, DeliveryIntentSubmitted, DeliveryIntentHeld)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		marked = n == 1
		return err
	})
	return marked, err
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
	if options.BusyDelivery == "steer" && !options.QueuesAsSteer() {
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
	options.SourceSessionID = senderSessionID
	queued, err := enqueueMessageTx(tx, r.now(), targetSessionID, message, deliveryKey, options, 0, QueueOperationMessage, queueIntent{Bind: intentID})
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	r.signalHeld(queued)
	if queued.DeliveryID == intentID {
		_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: targetSessionID, SourceSessionID: senderSessionID, QueueID: queued.ID, Message: message})
	}
	return queued.ID, queued.DeliveryID, nil
}

type deliveryIntentExecutor interface {
	deliveryIntentQuerier
	Exec(query string, args ...any) (sql.Result, error)
}

func failDeliveryIntent(q deliveryIntentExecutor, id int64, failure string) (bool, error) {
	if strings.TrimSpace(failure) == "" {
		return false, fmt.Errorf("delivery failure reason is required")
	}
	res, err := q.Exec(`UPDATE delivery_intents SET failed_from=status,status=?,evidence=?,failure=?,updated_at=datetime('now') WHERE id=? AND status IN (?,?,?,?,?)`, DeliveryIntentFailed, surface.EvidenceFailed, failure, id, DeliveryIntentSubmitted, DeliveryIntentSent, DeliveryIntentQueued, DeliveryIntentUnknown, DeliveryIntentHeld)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func insertQueuedDeliveryIntent(tx *sql.Tx, senderSessionID, targetSessionID, message string, queueID int64, relayed bool) (int64, error) {
	res, err := tx.Exec(`INSERT INTO delivery_intents(uuid,sender_session_id,target_session_id,message,queue_id,relayed,status,evidence) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), senderSessionID, targetSessionID, boundedIntentMessage(message), queueID, relayed, DeliveryIntentQueued, surface.EvidenceQueued)
	if err != nil {
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

type deliveryIntentQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func deliveryIntentByID(q deliveryIntentQuerier, id int64) (*DeliveryIntent, error) {
	return scanDeliveryIntent(q.QueryRow(`SELECT id,uuid,sender_session_id,target_session_id,provider_key,message,queue_id,relayed,status,evidence,failure,retries,created_at,updated_at FROM delivery_intents WHERE id=?`, id))
}

func deliveryIntentByProviderKey(q deliveryIntentQuerier, targetSessionID, providerKey string) (*DeliveryIntent, bool, error) {
	intent, err := scanDeliveryIntent(q.QueryRow(`SELECT id,uuid,sender_session_id,target_session_id,provider_key,message,queue_id,relayed,status,evidence,failure,retries,created_at,updated_at FROM delivery_intents WHERE target_session_id=? AND provider_key=? AND provider_key!=''`, targetSessionID, providerKey))
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	return intent, err == nil, err
}

func scanDeliveryIntent(row *sql.Row) (*DeliveryIntent, error) {
	var intent DeliveryIntent
	var status, evidence, createdAt, updatedAt string
	if err := row.Scan(&intent.ID, &intent.UUID, &intent.SenderSessionID, &intent.TargetSessionID, &intent.ProviderKey, &intent.Message, &intent.QueueID, &intent.Relayed, &status, &evidence, &intent.Failure, &intent.Retries, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	intent.Status = DeliveryIntentStatus(status)
	intent.Evidence = surface.DeliveryEvidence(evidence)
	var err error
	intent.CreatedAt, err = parseSQLiteTimestamp(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse delivery intent creation timestamp: %w", err)
	}
	intent.UpdatedAt, err = parseSQLiteTimestamp(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse delivery intent update timestamp: %w", err)
	}
	return &intent, nil
}

const maxIntentMessage = 16 * 1024

func boundedIntentMessage(message string) string {
	if len(message) <= maxIntentMessage {
		return message
	}
	return message[:maxIntentMessage]
}

// DeleteFailedDelivery removes a failure the target never took: its dead queue row is canceled, so neither a retry nor the queue brings it back. The delivery history keeps it.
func (r *Registry) DeleteFailedDelivery(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var targetSessionID, message string
	var queueID int64
	var deletable bool
	if err := tx.QueryRow(`SELECT di.target_session_id,di.message,di.queue_id,(`+deletableFailure+`) FROM delivery_intents di WHERE di.id=?`, id).Scan(&targetSessionID, &message, &queueID, &deletable); err != nil {
		return err
	}
	if !deletable {
		return ErrDeliveryNotDeletable
	}
	canceled := false
	if queueID != 0 {
		res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE id=? AND status='dead'`, queueID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		canceled = n == 1
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if canceled {
		_ = r.RecordHistory(HistoryEntry{Kind: "canceled", SessionID: targetSessionID, QueueID: queueID, Message: message, Result: "deleted after it failed"})
	}
	return nil
}

func (r *Registry) RecordDeliveryFailure(id int64, failure string) (bool, error) {
	if id == 0 {
		return false, fmt.Errorf("delivery failure requires an intent")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := failDeliveryIntent(tx, id, failure)
	if err != nil || !changed {
		return false, err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func retryQueuedDeliveryIntent(tx *sql.Tx, queueID int64) error {
	_, err := tx.Exec(`UPDATE delivery_intents SET status=?,evidence=?,failure='',retries=retries+1,updated_at=datetime('now') WHERE queue_id=? AND status IN (?,?)`, DeliveryIntentQueued, surface.EvidenceQueued, queueID, DeliveryIntentFailed, DeliveryIntentUnknown)
	return err
}

func finishQueuedDeliveryIntent(tx *sql.Tx, queueID int64, failure string) error {
	id, err := queuedDeliveryIntentID(tx, queueID)
	if err != nil || id == 0 {
		return err
	}
	_, err = failDeliveryIntent(tx, id, failure)
	return err
}
