package voice

import (
	"context"
	"errors"
	"strings"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type follower struct {
	attemptID string
	target    surface.Session
	// ctx ends with the target or the call, so nothing is said about a session voice has left.
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{}
	err      error
	voice    map[string][]string
	pending  int
	settled  chan struct{}
	unplaced []string
	typed    map[string]*typedTurn
	retired  map[string]bool
}

// followTarget replaces the current follower with one for target. The caller holds s.mu.
func (s *Service) followTarget(target *surface.Session) {
	s.stopFollowing()
	if target == nil || s.stream == nil || !s.active() {
		return
	}
	ctx, cancel := s.callContext(s.background)
	f := &follower{attemptID: s.state.State.AttemptID, target: *target, ctx: ctx, cancel: cancel, ready: make(chan struct{}), voice: map[string][]string{}, settled: make(chan struct{}), typed: map[string]*typedTurn{}, retired: map[string]bool{}}
	if !s.spawn(func() { s.runFollower(ctx, f) }) {
		cancel()
		return
	}
	s.follow = f
}

// stopFollowing ends the current follower. The caller holds s.mu.
func (s *Service) stopFollowing() {
	if s.follow != nil {
		s.follow.cancel()
		s.follow = nil
	}
	s.replies = nil
}

func (s *Service) runFollower(ctx context.Context, f *follower) {
	defer f.cancel()
	subscription, ok := s.subscribe(ctx, f)
	if !ok {
		return
	}
	defer subscription.Cancel()
	lastBody := map[string]string{}
	seenVersion := map[string]uint64{}
	for {
		var event sessionstream.Event
		var open bool
		select {
		case <-ctx.Done():
			return
		case event, open = <-subscription.Events:
		}
		if !open {
			s.followerFailed(f, errors.New("the session journal ended"))
			return
		}
		if event.Kind == "source-error" {
			reason := event.Reason
			if reason == "" {
				reason = "the session journal source failed"
			}
			s.followerFailed(f, errors.New(reason))
			s.refollow(f, reason)
			return
		}
		if event.TurnID == "" {
			continue
		}
		if prior, found := seenVersion[event.ItemID]; found && ((event.Version > 0 && prior > 0 && event.Version <= prior) || (event.Version == 0 && lastBody[event.ItemID] == event.Body)) {
			continue
		}
		seenVersion[event.ItemID] = event.Version
		messages, typed, ok := s.place(ctx, f, event)
		if !ok {
			return
		}
		previous := lastBody[event.ItemID]
		lastBody[event.ItemID] = event.Body
		switch {
		case typed != nil:
			s.followTyped(f, event, typed)
		case messages != nil:
			s.followVoiceTurn(ctx, f, messages, event, previous)
		}
		if event.Terminal() {
			s.endTurn(f, event.TurnID)
			if messages != nil || typed != nil {
				s.refreshBrief(ctx, f)
			}
		}
	}
}

func (s *Service) subscribe(ctx context.Context, f *follower) (sessionstream.Subscription, bool) {
	subscription, err := s.stream.PrepareSessionStream(ctx, &f.target)
	s.mu.Lock()
	f.err = err
	close(f.ready)
	if err == nil {
		s.unlock()
		return subscription, true
	}
	if s.follow != f {
		s.unlock()
		return subscription, false
	}
	s.state.State.Message = "Voice cannot follow " + f.target.Name + ": " + err.Error()
	s.appendEvent(Event{Method: "voice/target/follow-failed", Params: map[string]any{"targetId": f.target.ID, "error": err.Error()}})
	_ = s.commit()
	return subscription, false
}

func (s *Service) followVoiceTurn(ctx context.Context, f *follower, messages []string, event sessionstream.Event, previous string) {
	if event.SpokenAssistantContent() {
		delta := event.Body
		if strings.HasPrefix(event.Body, previous) {
			delta = strings.TrimPrefix(event.Body, previous)
		}
		if strings.TrimSpace(delta) != "" {
			stage := "interim"
			if event.Final {
				stage = "final"
			}
			s.speakDelegation(ctx, f.attemptID, messages[0], &f.target, event.TurnID, delta, stage)
		}
	}
	if event.Terminal() && event.Failed() {
		for _, messageID := range messages {
			s.recordDelegationFailure(f.attemptID, messageID, f.target.ID, errors.New(event.FailureReason()))
		}
	}
}

func (s *Service) endTurn(f *follower, turnID string) {
	s.mu.Lock()
	delete(f.voice, turnID)
	delete(f.typed, turnID)
	delete(f.retired, turnID)
	s.unlock()
}

// Carries the retired turns so a recovered source cannot recast them as typed.
func (s *Service) refollow(f *follower, reason string) {
	s.mu.Lock()
	if s.follow != f {
		s.unlock()
		return
	}
	replies := s.replies
	s.followTarget(&f.target)
	if next := s.follow; next != nil {
		next.retired = f.retired
		for i := range replies {
			replies[i].f = next
		}
		s.replies = replies
	}
	s.state.State.Message = "The session's journal failed and voice is following it again: " + reason
	s.appendEvent(Event{Method: "voice/target/refollowed", Params: map[string]any{"targetId": f.target.ID, "error": reason}})
	_ = s.commit()
}

func (s *Service) followerFailed(f *follower, err error) {
	s.mu.Lock()
	var messages []string
	for turnID, ids := range f.voice {
		messages = append(messages, ids...)
		delete(f.voice, turnID)
		f.retired[turnID] = true
	}
	s.unlock()
	for _, messageID := range messages {
		s.recordDelegationFailure(f.attemptID, messageID, f.target.ID, err)
	}
}

// The caller holds s.mu.
func (s *Service) settleDelegation(f *follower, messageID, turnID, text string) {
	f.pending--
	switch {
	case turnID != "":
		f.voice[turnID] = append(f.voice[turnID], messageID)
	case text != "":
		f.unplaced = append(f.unplaced, text)
		if len(f.unplaced) > queuedRepliesMax {
			f.unplaced = f.unplaced[1:]
		}
	}
	close(f.settled)
	f.settled = make(chan struct{})
}
