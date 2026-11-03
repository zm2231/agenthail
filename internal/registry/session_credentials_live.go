package registry

import (
	"database/sql"
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
