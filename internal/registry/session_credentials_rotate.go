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

type LiveCredential struct {
	Capabilities []string
	Lead         bool
	Delivered    bool
}

// MarkCredentialDelivered reports whether the token is still the session's live one, recording its delivery if so.
func (r *Registry) MarkCredentialDelivered(sessionID, tokenHash string) (bool, error) {
	result, err := r.db.Exec(`UPDATE session_credentials SET delivered=1 WHERE session_id=? AND token_hash=? AND revoked_at_ms=0`, sessionID, tokenHash)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (r *Registry) LiveCredentials() (map[string]LiveCredential, error) {
	rows, err := r.read.Query(`SELECT sc.session_id, sc.capabilities, sc.session_id IN (` + localLeadSessions + `), sc.delivered FROM session_credentials sc WHERE sc.revoked_at_ms=0 AND sc.session_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[string]LiveCredential{}
	for rows.Next() {
		var id, encoded string
		var credential LiveCredential
		if err := rows.Scan(&id, &encoded, &credential.Lead, &credential.Delivered); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &credential.Capabilities); err != nil {
			return nil, err
		}
		live[id] = credential
	}
	return live, rows.Err()
}
