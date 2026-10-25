package voice

import (
	"context"
	"errors"
	"strings"
)

// Apply runs one phone action. s.mu guards only state and is never held
// across I/O; s.op keeps actions that do I/O in arrival order, so a hangup, a
// heartbeat, View and the event poll never wait behind one.
func (s *Service) Apply(ctx context.Context, owner string, a Action) (State, error) {
	s.mu.Lock()
	if s.closed {
		defer s.unlock()
		return s.view(owner), errors.New("voice is shutting down")
	}
	if s.loadErr != nil && a.Action != "stop" {
		defer s.unlock()
		return s.view(owner), s.loadErr
	}
	if s.provider == nil {
		defer s.unlock()
		return s.view(owner), errors.New("Codex voice is unavailable on this host")
	}
	if owner == "" {
		defer s.unlock()
		return s.view(owner), errors.New("a paired device identity is required")
	}
	if s.active() && s.state.Owner != owner {
		defer s.unlock()
		return s.view(owner), errors.New("another device owns this call")
	}
	if a.Action != "heartbeat" {
		s.seen()
	}
	s.applying.Add(1)
	s.unlock()
	defer s.applying.Done()
	err := s.apply(ctx, owner, a)
	return s.published.Load().view(owner), err
}

func (s *Service) apply(ctx context.Context, owner string, a Action) error {
	switch a.Action {
	case "heartbeat":
		s.mu.Lock()
		defer s.unlock()
		v := &s.state.State
		if !s.active() || a.AttemptID == "" || a.AttemptID != v.AttemptID {
			return errors.New("heartbeat requires the current active voice call")
		}
		s.state.NativeHeartbeatEnrolled = true
		s.seen()
		return nil
	case "input-state":
		s.mu.Lock()
		v := &s.state.State
		if a.AttemptID == "" || a.AttemptID != v.AttemptID || !s.active() || a.Muted == nil || a.InputRevision <= 0 {
			s.unlock()
			return errors.New("input state requires the current call, its mute state and a revision")
		}
		if a.InputRevision <= v.InputRevision {
			s.unlock()
			return nil
		}
		v.InputMuted, v.InputRevision = *a.Muted, a.InputRevision
		return s.commit()
	case "connected":
		s.mu.Lock()
		v := &s.state.State
		if a.AttemptID != v.AttemptID || v.Phase != "negotiating" {
			s.unlock()
			return errors.New("no matching negotiated call")
		}
		v.Phase = "connected"
		if s.connected != nil {
			close(s.connected)
			s.connected = nil
		}
		return s.commit()
	case "stop":
		return s.hangup(ctx, a.AttemptID)
	}
	s.op.Lock()
	defer s.op.Unlock()
	switch a.Action {
	case "prepare":
		return s.prepare(ctx)
	case "new":
		s.mu.Lock()
		if s.active() {
			s.unlock()
			return errors.New("end the current voice call before starting a new orchestrator conversation")
		}
		if s.state.State.Phase == "creating" {
			s.unlock()
			return errors.New("operator creation outcome is unknown; inspect Codex before creating another operator")
		}
		s.unlock()
		return s.createOperator(ctx)
	case "select":
		_, err := s.selectTarget(ctx, a.TargetID, selectForCall)
		return err
	case "transfer":
		s.mu.Lock()
		connected := s.state.State.Phase == "connected"
		s.unlock()
		if !connected {
			return errors.New("voice transfer requires the current connected call")
		}
		_, err := s.selectTarget(ctx, a.TargetID, transferByPhone)
		return err
	case "delegate":
		return s.delegate(ctx, a, false)
	case "target-interrupt":
		return s.interruptTarget(ctx, a)
	case "start":
		return s.start(ctx, owner, a)
	case "text":
		return s.text(ctx, a)
	case "interrupt":
		s.mu.Lock()
		if s.state.State.Session == nil {
			s.unlock()
			return errors.New("there is no operator to interrupt")
		}
		session := *s.state.State.Session
		s.unlock()
		return s.provider.Interrupt(ctx, &session)
	}
	return errors.New("unsupported voice action")
}

func (s *Service) text(ctx context.Context, a Action) error {
	s.mu.Lock()
	v := &s.state.State
	if v.Target != nil {
		s.unlock()
		return s.delegate(ctx, a, false)
	}
	if a.AttemptID != v.AttemptID || v.Phase != "connected" {
		s.unlock()
		return errors.New("text requires the current connected voice call")
	}
	if strings.TrimSpace(a.Text) == "" || len(a.Text) > 16<<10 || a.MessageID == "" || len(a.MessageID) > 128 {
		s.unlock()
		return errors.New("text and a unique messageId are required")
	}
	for _, id := range s.state.Messages {
		if id == a.MessageID {
			s.unlock()
			return errors.New("message already submitted; inspect its outcome before sending again")
		}
	}
	if len(s.state.Messages) >= 128 {
		s.unlock()
		return errors.New("this call reached its text-message limit; reconnect before sending more text")
	}
	s.state.Messages = append(s.state.Messages, a.MessageID)
	v.TextReceipt = "unknown:" + a.MessageID
	session := *v.Session
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	if err := s.commit(); err != nil {
		return err
	}
	if err := s.provider.Request(callCtx, &session, "thread/realtime/appendText", map[string]any{"threadId": session.ID, "text": a.Text, "role": "user"}); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.sameCall(a.AttemptID, session.ID, "") {
		s.unlock()
		return nil
	}
	v.TextReceipt = "accepted:" + a.MessageID
	return s.commit()
}
