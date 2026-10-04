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
	stamp := now.UTC().Format(time.RFC3339Nano)
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
	if err := tx.Commit(); err != nil {
		return ActionReceiptReservation{}, err
	}
	receipt.Principal = principal
	receipt.Key = key
	return ActionReceiptReservation{Receipt: receipt}, nil
}

func (r *Registry) CompleteActionReceipt(principal, key, requestHash string, receipt []byte, now time.Time) error {
	if err := validateActionReceiptInput(principal, key, requestHash); err != nil {
		return err
	}
	if len(receipt) > ActionReceiptBodyLimit {
		return fmt.Errorf("action receipt exceeds %d bytes", ActionReceiptBodyLimit)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := r.db.Exec(`UPDATE action_receipts SET status=?,receipt=?,updated_at=? WHERE idempotency_key=? AND request_hash=? AND status=?`, ActionReceiptCompleted, append([]byte(nil), receipt...), now.UTC().Format(time.RFC3339Nano), actionReceiptStorageKey(principal, key), requestHash, ActionReceiptPending)
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
