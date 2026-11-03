package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
)

func (r *Registry) LiveCredentialHash(sessionID string) (string, bool, error) {
	var hash string
	err := r.read.QueryRow(`SELECT token_hash FROM session_credentials WHERE session_id=? AND revoked_at_ms=0`, sessionID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

func (r *Registry) LiveCredentialSessions() ([]string, error) {
	rows, err := r.read.Query(`SELECT session_id FROM session_credentials WHERE revoked_at_ms=0 AND session_id IS NOT NULL ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Registry) LiveCredentialCapabilities(sessionID string) ([]string, bool, error) {
	var encoded string
	err := r.read.QueryRow(`SELECT capabilities FROM session_credentials WHERE session_id=? AND revoked_at_ms=0`, sessionID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var capabilities []string
	if err := json.Unmarshal([]byte(encoded), &capabilities); err != nil {
		return nil, false, err
	}
	return capabilities, true, nil
}
