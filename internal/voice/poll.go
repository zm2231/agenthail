package voice

import (
	"context"
	"encoding/json"
	"time"
)

const (
	// pollWait bounds how long one poll waits for the call's next events.
	pollWait = 5 * time.Second
	// heartbeatExpiry is how long a call goes without a heartbeat before
	// the phone counts as disconnected and the audio hangup is requested.
	heartbeatExpiry = 40 * time.Second
	// hangupGiveUp is how long after a phone disconnect the call waits for
	// Codex to confirm the audio hangup.
	hangupGiveUp = 30 * time.Second
)

type clock struct {
	now    func() time.Time
	ticker func() (<-chan time.Time, func())
}

var systemClock = clock{
	now: time.Now,
	ticker: func() (<-chan time.Time, func()) {
		t := time.NewTicker(time.Second)
		return t.C, t.Stop
	},
}

// observe starts the event poll for the current call. The caller holds s.mu.
func (s *Service) observe() {
	if s.running {
		return
	}
	s.running = s.spawn(func() {
		for {
			more, pause := s.pollOnce()
			if !more || pause && !s.pause() {
				return
			}
		}
	})
}

// pause waits one clock tick after a poll that failed or lost events, so a
// source that keeps failing or is gone is not asked again at once. It reports false once the service stops.
func (s *Service) pause() bool {
	ticks, stop := s.clock.ticker()
	defer stop()
	select {
	case <-s.background.Done():
	case _, open := <-ticks:
		if open {
			return true
		}
	}
	s.mu.Lock()
	s.stopObserving()
	s.unlock()
	return false
}

// wakePoll ends the poll in flight. The caller holds s.mu.
func (s *Service) wakePoll() {
	if s.interruptPoll != nil {
		s.interruptPoll()
	}
}

// stopObserving ends the event poll, the call's work and its event source.
// The caller holds s.mu, so a call started afterwards keeps its own.
func (s *Service) stopObserving() {
	s.running = false
	s.endCall()
	if s.state.State.Session != nil {
		s.release(s.state.State.Session.ID, s.cursor)
	}
}

// pollWait is how long the next poll may wait: pollWait, or less when the
// call's heartbeat expiry or hangup give-up falls due sooner, so that check
// runs on time. The caller holds s.mu.
func (s *Service) pollWait() time.Duration {
	due := time.Unix(0, s.lastSeen.Load()).Add(heartbeatExpiry)
	if !s.disconnectedAt.IsZero() {
		due = s.disconnectedAt.Add(hangupGiveUp)
	}
	if len(s.replies) > 0 && !s.replying && s.quietAt().Before(due) {
		due = s.quietAt()
	}
	// The checks fire once strictly past the deadline.
	return max(0, min(pollWait, due.Sub(s.clock.now())+time.Millisecond))
}

// pollOnce waits outside s.mu for the call's next events and applies them if
// the call is still the one polled. It reports false once the poll has ended,
// and whether the next poll should wait a tick because this one failed or
// lost events.
func (s *Service) pollOnce() (more, pause bool) {
	s.mu.Lock()
	v := &s.state.State
	if !s.active() {
		s.stopObserving()
		s.unlock()
		return false, false
	}
	cursor, threadID, attemptID, wait := s.cursor, v.Session.ID, v.AttemptID, s.pollWait()
	ctx, cancel := context.WithTimeout(s.background, wait+8*time.Second)
	s.interruptPoll = cancel
	s.unlock()
	b, err := s.provider.Poll(ctx, cursor, threadID, wait)
	cancel()
	if s.cycled != nil {
		defer s.cycled()
	}
	s.mu.Lock()
	s.interruptPoll = nil
	if s.rewait {
		s.rewait = false
		if err != nil && s.background.Err() == nil {
			b, err = Batch{Cursor: cursor}, nil
		}
	}
	if s.background.Err() != nil {
		s.stopObserving()
		s.unlock()
		return false, false
	}
	if v.AttemptID != attemptID || s.cursor != cursor {
		s.unlock()
		return true, false
	}
	changed := false
	var tools []Event
	if err != nil {
		v.Message = "Voice event connection interrupted: " + err.Error()
	} else {
		tools = s.applyBatch(b)
		changed = len(b.Events) > 0 || b.Lost
	}
	s.speakQueued()
	// After a phone disconnect the poll keeps running until Codex confirms the
	// hangup, or gives up once it has waited long enough.
	if s.active() && !s.disconnectedAt.IsZero() && s.clock.now().Sub(s.disconnectedAt) > hangupGiveUp {
		v.Phase = "unknown"
		v.Message = "Phone disconnected; Codex did not confirm the audio hangup. Agent work remains in the operator thread."
		s.stopObserving()
		_ = s.commit()
		for _, e := range tools {
			s.runTool(e)
		}
		return false, err != nil || b.Lost
	}
	disconnected := s.active() && s.disconnectedAt.IsZero() && s.clock.now().Sub(time.Unix(0, s.lastSeen.Load())) > heartbeatExpiry
	if disconnected {
		s.disconnectedAt = s.clock.now()
		v.TerminalReason = "phone_disconnected"
	}
	if changed {
		_ = s.commit()
	} else {
		s.unlock()
	}
	for _, e := range tools {
		s.runTool(e)
	}
	if disconnected {
		ctx, cancel := context.WithTimeout(s.background, 10*time.Second)
		_ = s.hangup(ctx, attemptID)
		cancel()
		s.mu.Lock()
		if v.AttemptID == attemptID {
			v.Message = "Phone disconnected; audio hangup requested. Agent work remains in the operator thread."
		}
		_ = s.commit()
	}
	return true, err != nil || b.Lost
}

// applyBatch applies polled events to the call and returns its tool calls.
// The caller holds s.mu.
func (s *Service) applyBatch(b Batch) []Event {
	v := &s.state.State
	if b.Lost {
		v.Truncated = true
		s.eventGap = true
		v.Phase, v.SDP = "unknown", ""
		v.Message = "Some live voice events were lost. Hang up, then inspect the operator timeline before retrying actions."
	}
	s.cursor = b.Cursor
	var tools []Event
	for _, e := range b.Events {
		if id, ok := e.Params["realtimeSessionId"].(string); ok && id != v.AttemptID {
			continue
		}
		if e.Method == "item/tool/call" {
			tools = append(tools, e)
		}
		switch e.Method {
		case "thread/realtime/started":
			id, _ := e.Params["realtimeSessionId"].(string)
			if id == v.AttemptID {
				s.boundAttempt = id
			}
		case "thread/realtime/sdp":
			if !s.eventGap && s.boundAttempt == v.AttemptID && (v.Phase == "starting" || v.Phase == "unknown") {
				v.Phase = "negotiating"
				v.SDP, _ = e.Params["sdp"].(string)
				v.Message = ""
			}
		case "thread/realtime/closed":
			v.Phase, v.SDP = "ended", ""
			s.endCall()
			if s.toolsOutdated() {
				s.state.ToolsNoticeShown = true
			}
		case "thread/realtime/error":
			v.Phase = "unknown"
			v.Message, _ = e.Params["message"].(string)
		}
		s.heard(e)
		if e.Method == "thread/realtime/sdp" || e.Method == "thread/realtime/outputAudio/delta" {
			continue
		}
		data, _ := json.Marshal(e)
		if len(data) > 16<<10 {
			v.Truncated = true
			continue
		}
		v.Events = append(v.Events, e)
		if len(v.Events) > 100 {
			v.Events = v.Events[len(v.Events)-100:]
			v.Truncated = true
		}
		if !b.Lost {
			s.queueSpokenTranscript(e)
		}
	}
	return tools
}
