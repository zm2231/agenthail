package registry

import (
	"database/sql"
	"errors"
	"fmt"
)

type OriginVia string

const (
	OriginPerson      OriginVia = "person"
	OriginAgent       OriginVia = "agent"
	OriginCoordinator OriginVia = "coordinator"
)

type SessionOrigin struct {
	SessionID string
	Via       OriginVia
	CreatorID string
}

func OriginOf(sessionID, creatorID string) SessionOrigin {
	if creatorID == "" {
		return SessionOrigin{SessionID: sessionID, Via: OriginPerson}
	}
	return SessionOrigin{SessionID: sessionID, Via: OriginAgent, CreatorID: creatorID}
}

// A session is started once, so a second record for it is ignored.
func (r *Registry) RecordSessionOrigin(origin SessionOrigin) error {
	if err := origin.valid(); err != nil {
		return err
	}
	_, err := r.db.Exec(`INSERT INTO session_origins(session_id,via,creator_session_id) VALUES(?,?,?) ON CONFLICT(session_id) DO NOTHING`, origin.SessionID, string(origin.Via), origin.CreatorID)
	return err
}

func (origin SessionOrigin) valid() error {
	if origin.SessionID == "" {
		return errors.New("session origin needs a session id")
	}
	switch origin.Via {
	case OriginAgent:
		if origin.CreatorID == "" {
			return errors.New("a session started by an agent names its creator")
		}
	case OriginPerson, OriginCoordinator:
		if origin.CreatorID != "" {
			return fmt.Errorf("a session started by %s names no creator session", origin.Via)
		}
	default:
		return fmt.Errorf("unknown session origin %q", origin.Via)
	}
	return nil
}

func (r *Registry) SessionOrigin(sessionID string) (SessionOrigin, bool, error) {
	origin := SessionOrigin{SessionID: sessionID}
	var via string
	err := r.read.QueryRow(`SELECT via,creator_session_id FROM session_origins WHERE session_id=?`, sessionID).Scan(&via, &origin.CreatorID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionOrigin{}, false, nil
	}
	if err != nil {
		return SessionOrigin{}, false, err
	}
	origin.Via = OriginVia(via)
	return origin, true, nil
}
