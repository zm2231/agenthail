package registry

import (
	"database/sql"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type PersonMessage struct {
	At      time.Time
	Message string
}

// PersonLastMessage is the person's latest message, not failed, that this
// host delivered to leadSessionID or to one of its own sessions in projectID.
func (r *Registry) PersonLastMessage(leadSessionID, projectID string) (*PersonMessage, error) {
	var at, message string
	err := r.read.QueryRow(`SELECT created_at, message FROM delivery_intents
		WHERE sender_session_id=? AND status<>? AND (target_session_id=? OR target_session_id IN (
			SELECT cs.session_id FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
			WHERE s.host_id='' AND ?<>'' AND (json_extract(cs.host_project,'$.id')=? OR json_extract(cs.host_project,'$.localId')=?)))
		ORDER BY id DESC LIMIT 1`, surface.PersonSessionID, DeliveryIntentFailed, leadSessionID, projectID, projectID, projectID).Scan(&at, &message)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	when, err := parseSQLiteTimestamp(at)
	if err != nil {
		return nil, err
	}
	return &PersonMessage{At: when, Message: message}, nil
}
