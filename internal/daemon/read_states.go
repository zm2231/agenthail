package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) recordReadStates(sessions []surface.Session, observedAt time.Time) {
	states := map[string]surface.ReadState{}
	for _, session := range sessions {
		if session.ReadState != nil {
			states[session.ID] = *session.ReadState
		}
	}
	if err := d.Registry.RecordCodexReadStates(states, observedAt); err != nil {
		d.log.Printf("catalog read states: %s", err)
	}
}
