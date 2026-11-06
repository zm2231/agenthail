package registry

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) RecordStartingSession(session surface.Session, alias AliasReservation, origin SessionOrigin) error {
	if err := r.RegisterSession(session); err != nil {
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
