package voice

import "github.com/zm2231/agenthail/internal/sessionstream"

// The caller holds s.mu.
func (s *Service) claimPeerTurn(f *follower, event sessionstream.Event) {
	frame, turn := event.MessageID, event.TurnID
	if frame == "" || turn == "" || frame == turn {
		return
	}
	if messages, ok := f.voice[frame]; ok {
		delete(f.voice, frame)
		delete(f.typed, turn)
		f.voice[turn] = append(f.voice[turn], messages...)
		s.retargetRequests(frame, turn)
		return
	}
	if f.retired[frame] {
		delete(f.retired, frame)
		_, voiced := f.voice[turn]
		_, typed := f.typed[turn]
		if !voiced && !typed {
			f.retired[turn] = true
		}
	}
}

// The caller holds s.mu.
func (s *Service) retargetRequests(from, to string) {
	for i := range s.state.Requests {
		if s.state.Requests[i].TurnID == from {
			s.state.Requests[i].TurnID = to
		}
	}
}
