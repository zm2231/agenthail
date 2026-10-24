package voice

import (
	"context"
	"errors"
	"strings"
)

const finishedWithoutReply = "The earlier request finished without a written reply."

// Runs after subscribing, so a turn ended since the brief is not missed.
func (s *Service) catchUp(ctx context.Context, f *follower) bool {
	s.mu.Lock()
	connected, waiting := s.connected, s.state.State.Phase != "connected"
	earlier, briefer := f.earlier, s.briefer
	f.earlier = nil
	s.unlock()
	if waiting {
		select {
		case <-ctx.Done():
			return false
		case <-connected:
		}
	}
	if len(earlier) == 0 {
		return true
	}
	turns := make([]string, 0, len(earlier))
	for _, r := range earlier {
		turns = append(turns, r.TurnID)
	}
	var outcomes map[string]TurnOutcome
	if briefer != nil {
		outcomes, _ = briefer.TurnOutcomes(ctx, &f.target, turns)
	}
	var ended []requestRecord
	s.mu.Lock()
	for _, r := range earlier {
		if outcomes[r.TurnID].Done {
			f.retired[r.TurnID] = true
			ended = append(ended, r)
			continue
		}
		f.voice[r.TurnID] = append(f.voice[r.TurnID], r.MessageID)
	}
	s.unlock()
	for _, r := range ended {
		if s.tellEnded(ctx, f, r, outcomes[r.TurnID]) {
			s.mu.Lock()
			s.requestDone(r.TurnID)
			_ = s.commit()
		}
	}
	return ctx.Err() == nil
}

func (s *Service) tellEnded(ctx context.Context, f *follower, r requestRecord, outcome TurnOutcome) bool {
	switch {
	case outcome.Failed != "":
		s.recordDelegationFailure(f.attemptID, r.MessageID, f.target.ID, errors.New(outcome.Failed))
		return ctx.Err() == nil
	case strings.TrimSpace(outcome.Reply) != "":
		return s.speakDelegation(ctx, f.attemptID, r.MessageID, &f.target, r.TurnID, outcome.Reply, "final")
	default:
		return s.speakDelegation(ctx, f.attemptID, r.MessageID, &f.target, r.TurnID, finishedWithoutReply, "final")
	}
}
