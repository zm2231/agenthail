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

func (r *Registry) LiveCredentials() (map[string][]string, error) {
	rows, err := r.read.Query(`SELECT session_id, capabilities FROM session_credentials WHERE revoked_at_ms=0 AND session_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[string][]string{}
	for rows.Next() {
		var id, encoded string
		if err := rows.Scan(&id, &encoded); err != nil {
			return nil, err
		}
		var capabilities []string
		if err := json.Unmarshal([]byte(encoded), &capabilities); err != nil {
			return nil, err
		}
		live[id] = capabilities
	}
	return live, rows.Err()
}
