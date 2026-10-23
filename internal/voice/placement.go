package voice

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
)

// unattributedWait bounds how long an unplaced turn waits on an unsettled delegation.
const unattributedWait = 30 * time.Second

// place returns the voice messages or typed turn an event belongs to, or
// neither for a retired turn, and false once the follower ends.
func (s *Service) place(ctx context.Context, f *follower, event sessionstream.Event) ([]string, *typedTurn, bool) {
	var deadline time.Time
	var ticks <-chan time.Time
	for {
		s.mu.Lock()
		if messages, ok := f.voice[event.TurnID]; ok {
			s.unlock()
			return messages, nil, true
		}
		if turn, ok := f.typed[event.TurnID]; ok {
			s.unlock()
			return nil, turn, true
		}
		if f.retired[event.TurnID] {
			s.unlock()
			return nil, nil, true
		}
		if f.pending == 0 || (!deadline.IsZero() && !s.clock.now().Before(deadline)) {
			if f.pending > 0 && s.follow == f {
				s.logUnattributed(f, event.TurnID)
			}
			turn := &typedTurn{}
			f.typed[event.TurnID] = turn
			s.unlock()
			return nil, turn, true
		}
		if deadline.IsZero() {
			deadline = s.clock.now().Add(unattributedWait)
			var stop func()
			ticks, stop = s.clock.ticker()
			defer stop()
		}
		settled := f.settled
		s.unlock()
		select {
		case <-ctx.Done():
			return nil, nil, false
		case <-settled:
		case <-ticks:
		}
	}
}

// The caller holds s.mu, so the log line runs after it is released.
func (s *Service) logUnattributed(f *follower, turnID string) {
	if logf := s.logf; logf != nil {
		targetID, pending := f.target.ID, f.pending
		s.later(func() {
			logf("voice: turn %s of %s taken as typed after waiting %s for %d delegation(s) to settle", turnID, targetID, unattributedWait, pending)
		})
	}
	s.appendEvent(Event{Method: "voice/target/turn-unattributed", Params: map[string]any{"targetId": f.target.ID, "turnId": turnID}})
	s.stage()
}
