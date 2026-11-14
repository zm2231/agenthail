package registry

import (
	"database/sql"
	"errors"
	"time"
)

func (r *Registry) LiveCredentialHash(sessionID string, now time.Time) (string, bool, error) {
	var hash string
	err := r.read.QueryRow(`SELECT token_hash FROM session_credentials WHERE session_id=? AND revoked_at_ms=0 AND max_expires_at_ms>?`, sessionID, now.UnixMilli()).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

func (r *Registry) SessionCredentialIssued(sessionID string) (bool, error) {
	var issued bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_credentials WHERE session_id=?)`, sessionID).Scan(&issued)
	return issued, err
}
