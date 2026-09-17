package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type APIActionReservation struct {
	IdempotencyKey  string
	SessionID       string
	Action          string
	Message         string
	SourceSessionID string
	Status          string
	Receipt         json.RawMessage
}

func (r *Registry) ReserveAPIAction(key, sessionID, action, message, sourceSessionID string) (*APIActionReservation, bool, error) {
	if key == "" || sessionID == "" || action == "" {
		return nil, false, fmt.Errorf("idempotency key, session id, and action are required")
	}
	result, err := r.db.Exec(`INSERT OR IGNORE INTO api_action_reservations (idempotency_key,session_id,action,message,source_session_id) VALUES (?,?,?,?,?)`, key, sessionID, action, message, sourceSessionID)
	if err != nil {
		return nil, false, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	reservation, err := r.APIAction(key)
	if err != nil {
		return nil, false, err
	}
	return reservation, created == 1, nil
}

func (r *Registry) APIAction(key string) (*APIActionReservation, error) {
	var reservation APIActionReservation
	var receipt string
	err := r.db.QueryRow(`SELECT idempotency_key,session_id,action,message,source_session_id,status,receipt FROM api_action_reservations WHERE idempotency_key=?`, key).Scan(&reservation.IdempotencyKey, &reservation.SessionID, &reservation.Action, &reservation.Message, &reservation.SourceSessionID, &reservation.Status, &receipt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	reservation.Receipt = json.RawMessage(receipt)
	return &reservation, nil
}

func (r *Registry) CompleteAPIAction(key, status string, receipt any) error {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	result, err := r.db.Exec(`UPDATE api_action_reservations SET status=?, receipt=?, updated_at=datetime('now') WHERE idempotency_key=?`, status, payload, key)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("API action reservation %q was not found", key)
	}
	return nil
}
