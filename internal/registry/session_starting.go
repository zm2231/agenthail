package registry

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) RecordStartingSession(session surface.Session, alias AliasReservation, origin SessionOrigin) error {
	if err := r.RecordObservedSession(session); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if err := r.BindReservedAlias(alias, session.ID); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if err := r.RecordSessionOrigin(origin); err != nil {
		return fmt.Errorf("record who started it: %w", err)
	}
	return nil
}

// RecordLearnedSession records a session an action returned. Read is the row
// as the action read or created it, or nil when it has none; the action then
// writes only the fields it learned since, over whatever discovery recorded
// meanwhile. A session with no read row, or a different id, is recorded whole.
func (r *Registry) RecordLearnedSession(read *surface.Session, session surface.Session) error {
	if read == nil || read.ID != session.ID {
		return r.RecordObservedSession(session)
	}
	return r.UpdateSession(session, LearnedSessionUpdate(*read, session))
}
