package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ActionReceiptPending   = "pending"
	ActionReceiptCompleted = "completed"
	// A retry receipt holds an answer that was not the action's outcome, such as
	// a paired host that did not answer; the next request with its key runs again.
	ActionReceiptRetry     = "retry"
	ActionReceiptKeyLimit  = 128
	ActionReceiptHashSize  = 64
	ActionReceiptBodyLimit = 256 << 10
)

var ErrActionReceiptInvalid = errors.New("invalid action receipt")

type ActionReceipt struct {
	Principal   string
	Key         string
	RequestHash string
	Status      string
	Receipt     []byte
	CreatedAt   string
	UpdatedAt   string
}

type ActionReceiptReservation struct {
	Acquired bool
	Receipt  ActionReceipt
}

func (r *Registry) ReserveActionReceipt(principal, key, requestHash string, now time.Time) (ActionReceiptReservation, error) {
	if err := validateActionReceiptInput(principal, key, requestHash); err != nil {
		return ActionReceiptReservation{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	storageKey := actionReceiptStorageKey(principal, key)
	stamp := formatTimestamp(now)
	tx, err := r.db.Begin()
	if err != nil {
		return ActionReceiptReservation{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT OR IGNORE INTO action_receipts(idempotency_key,request_hash,status,receipt,created_at,updated_at) VALUES(?,?,?,?,?,?)`, storageKey, requestHash, ActionReceiptPending, []byte{}, stamp, stamp)
	if err != nil {
		return ActionReceiptReservation{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return ActionReceiptReservation{}, err
	}
	if inserted == 1 {
		if err := tx.Commit(); err != nil {
			return ActionReceiptReservation{}, err
		}
		return ActionReceiptReservation{Acquired: true, Receipt: ActionReceipt{Principal: principal, Key: key, RequestHash: requestHash, Status: ActionReceiptPending, CreatedAt: stamp, UpdatedAt: stamp}}, nil
	}
	receipt, err := actionReceiptByStorageKey(tx, storageKey)
	if err != nil {
		return ActionReceiptReservation{}, err
	}
	acquired := receipt.Status == ActionReceiptRetry && receipt.RequestHash == requestHash
	if acquired {
		if _, err := tx.Exec(`UPDATE action_receipts SET status=?,updated_at=? WHERE idempotency_key=?`, ActionReceiptPending, stamp, storageKey); err != nil {
			return ActionReceiptReservation{}, err
		}
		receipt.Status, receipt.UpdatedAt = ActionReceiptPending, stamp
	}
	if err := tx.Commit(); err != nil {
		return ActionReceiptReservation{}, err
	}
	receipt.Principal = principal
	receipt.Key = key
	return ActionReceiptReservation{Acquired: acquired, Receipt: receipt}, nil
}

func (r *Registry) ActionReceipt(principal, key string) (ActionReceipt, bool, error) {
	receipt, err := actionReceiptByStorageKey(r.db, actionReceiptStorageKey(principal, key))
	if errors.Is(err, sql.ErrNoRows) {
		return ActionReceipt{}, false, nil
	}
	if err != nil {
		return ActionReceipt{}, false, err
	}
	receipt.Principal, receipt.Key = principal, key
	return receipt, true, nil
}

func (r *Registry) CompleteActionReceipt(principal, key, requestHash string, receipt []byte, now time.Time) error {
	return r.settleActionReceipt(principal, key, requestHash, ActionReceiptCompleted, receipt, now)
}

// The retry receipt keeps what the caller needs to repeat the attempt
// exactly; the next reservation of the key returns it.
func (r *Registry) RetryActionReceipt(principal, key, requestHash string, attempt []byte, now time.Time) error {
	return r.settleActionReceipt(principal, key, requestHash, ActionReceiptRetry, attempt, now)
}

func (r *Registry) settleActionReceipt(principal, key, requestHash, status string, receipt []byte, now time.Time) error {
	if err := validateActionReceiptInput(principal, key, requestHash); err != nil {
		return err
	}
	if len(receipt) > ActionReceiptBodyLimit {
		return fmt.Errorf("action receipt exceeds %d bytes", ActionReceiptBodyLimit)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := r.db.Exec(`UPDATE action_receipts SET status=?,receipt=?,updated_at=? WHERE idempotency_key=? AND request_hash=? AND status=?`, status, append([]byte{}, receipt...), formatTimestamp(now), actionReceiptStorageKey(principal, key), requestHash, ActionReceiptPending)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// Pending receipts are kept: their run may still be going, and a resend has to
// learn that it was interrupted.
func (r *Registry) PruneActionReceipts(cutoff time.Time, limit int) (int64, error) {
	result, err := r.db.Exec(`DELETE FROM action_receipts WHERE idempotency_key IN (SELECT idempotency_key FROM action_receipts WHERE status IN (?,?) AND updated_at<? LIMIT ?)`, ActionReceiptCompleted, ActionReceiptRetry, formatTimestamp(cutoff), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func actionReceiptByStorageKey(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, storageKey string) (ActionReceipt, error) {
	var receipt ActionReceipt
	var body []byte
	if err := q.QueryRow(`SELECT request_hash,status,receipt,created_at,updated_at FROM action_receipts WHERE idempotency_key=?`, storageKey).Scan(&receipt.RequestHash, &receipt.Status, &body, &receipt.CreatedAt, &receipt.UpdatedAt); err != nil {
		return ActionReceipt{}, err
	}
	receipt.Receipt = append([]byte(nil), body...)
	return receipt, nil
}

func validateActionReceiptInput(principal, key, requestHash string) error {
	if strings.TrimSpace(principal) == "" || len(principal) > ActionReceiptKeyLimit {
		return fmt.Errorf("%w: principal", ErrActionReceiptInvalid)
	}
	if strings.TrimSpace(key) == "" || len(key) > ActionReceiptKeyLimit {
		return fmt.Errorf("%w: key", ErrActionReceiptInvalid)
	}
	if len(requestHash) != ActionReceiptHashSize {
		return fmt.Errorf("%w: request hash", ErrActionReceiptInvalid)
	}
	return nil
}

func actionReceiptStorageKey(principal, key string) string {
	return fmt.Sprintf("%d:%s%s", len(principal), principal, key)
}
