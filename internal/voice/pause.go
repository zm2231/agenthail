package voice

import (
	"context"
	"time"
)

const (
	quietGap = 2 * time.Second
	// speechStale caps how long a delta whose done was lost holds replies back.
	speechStale      = 4 * time.Second
	queuedRepliesMax = 8
)

type queuedReply struct {
	f                         *follower
	attemptID, targetID, text string
}

// The caller holds s.mu.
func (s *Service) queueReply(r queuedReply) {
	if len(s.replies) >= queuedRepliesMax {
		s.appendEvent(Event{Method: "voice/target/reply-dropped", Params: map[string]any{"targetId": r.targetID}})
		s.replies = s.replies[1:]
	}
	s.replies = append(s.replies, r)
	s.speakQueued()
	if len(s.replies) > 0 && s.interruptPoll != nil {
		s.rewait = true
		s.wakePoll()
	}
}

// The caller holds s.mu.
func (s *Service) heard(e Event) {
	role, _ := e.Params["role"].(string)
	switch e.Method {
	case "thread/realtime/transcript/delta":
		s.lastSpeech = s.clock.now()
		if role == "user" {
			s.listening = true
		}
	case "thread/realtime/transcript/done":
		s.lastSpeech = s.clock.now()
		if role == "user" {
			s.listening = false
		}
	case "thread/realtime/outputAudio/delta":
		s.lastSpeech = s.clock.now()
	}
}

// The caller holds s.mu.
func (s *Service) quietAt() time.Time {
	if s.listening {
		return s.lastSpeech.Add(max(quietGap, speechStale))
	}
	return s.lastSpeech.Add(quietGap)
}

// speakQueued speaks the oldest queued reply once the call is paused, one at a time. The caller holds s.mu.
func (s *Service) speakQueued() {
	if s.replying || len(s.replies) == 0 || s.clock.now().Before(s.quietAt()) {
		return
	}
	next := s.replies[0]
	s.replies = s.replies[1:]
	v := &s.state.State
	if s.follow != next.f || next.attemptID != v.AttemptID || v.Phase != "connected" || v.Session == nil || v.Target == nil || v.Target.ID != next.targetID {
		s.speakQueued()
		return
	}
	session := *v.Session
	ctx, cancel := context.WithTimeout(next.f.ctx, s.speechTimeout)
	s.replying = s.spawn(func() {
		defer cancel()
		err := s.provider.Request(ctx, &session, "thread/realtime/appendText", map[string]any{"threadId": session.ID, "text": next.text, "role": "developer"})
		s.mu.Lock()
		s.replying = false
		stage := "spoken"
		if err != nil {
			stage = "failed"
		}
		if s.sameCall(next.attemptID, session.ID, next.targetID) {
			s.appendEvent(Event{Method: "voice/target/reply-" + stage, Params: map[string]any{"targetId": next.targetID}})
		}
		s.speakQueued()
		_ = s.commit()
	})
	if !s.replying {
		cancel()
	}
}
