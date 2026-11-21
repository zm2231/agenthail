package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// A pass shows the receipts it listed, though they are stored only once the pass has registered their sessions.
func listedReadStates(sessions []surface.Session, view catalogViewState) map[string]surface.ReadState {
	states := map[string]surface.ReadState{}
	for _, session := range sessions {
		if session.ReadState != nil {
			states[session.ID] = *session.ReadState
			view.reads[session.ID] = *session.ReadState
		}
	}
	return states
}

func (d *Daemon) recordReadStates(states map[string]surface.ReadState, observedAt time.Time) {
	if err := d.Registry.RecordCodexReadStates(states, observedAt); err != nil {
		d.log.Printf("catalog read states: %s", err)
	}
}
