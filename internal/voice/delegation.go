package voice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
)

// delegatedSpeechTimeout lets a busy Codex delay an update rather than the call.
const delegatedSpeechTimeout = 15 * time.Second

// The caller holds s.op; the call's follower speaks the reply.
func (s *Service) delegate(ctx context.Context, a Action, spoken bool) error {
	s.mu.Lock()
	v := &s.state.State
	if err := s.delegationAllowed(a, spoken); err != nil {
		s.unlock()
		return err
	}
	targetID := v.Target.ID
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	s.unlock()
	target, err := s.target(callCtx, targetID)
	if err != nil {
		return err
	}
	if target == nil || target.Session == nil || target.Adapter == nil {
		return errors.New("selected target is no longer available")
	}
	if !target.Adapter.Capabilities().Stream {
		return errors.New("selected target cannot return correlated voice updates")
	}
	f, err := s.readyFollower(callCtx, targetID)
	if err != nil {
		return err
	}
	waker, ok := target.Adapter.(surface.Waker)
	woke := ok && waker.NeedsWake(callCtx, target.Session)
	s.mu.Lock()
	if err := s.delegationAllowed(a, spoken); err != nil || v.Target.ID != targetID || s.follow != f {
		s.unlock()
		if err == nil {
			err = errors.New("the voice target changed before the request was sent")
		}
		return err
	}
	s.state.Messages = append(s.state.Messages, a.MessageID)
	attemptID, sourceID := v.AttemptID, v.Session.ID
	if woke {
		v.Message = "Waking up " + target.Session.Name + "."
	}
	f.pending++
	s.stage()
	s.unlock()
	if err := s.flush(); err != nil {
		s.mu.Lock()
		s.settleDelegation(f, a.MessageID, "", "")
		s.unlock()
		return err
	}
	addressed := *target.Session
	receipt, err := s.dispatcher.DeliverWithoutQueue(callCtx, target.Adapter, target.Session, a.Text, "voice:"+attemptID+":"+a.MessageID, surface.SendOptions{SourceSessionID: sourceID, Author: surface.AuthorPerson})
	s.mu.Lock()
	held := ""
	switch {
	case err != nil:
		s.settleDelegation(f, a.MessageID, "", "")
	default:
		if held = heldReason(target, addressed, receipt); held != "" {
			s.settleDelegation(f, a.MessageID, "", a.Text)
		} else {
			s.settleDelegation(f, a.MessageID, receipt.TurnID, "")
		}
	}
	if !s.sameCall(attemptID, sourceID, targetID) {
		s.unlock()
		if err == nil {
			err = errors.New("the call ended or moved before the request's reply could be followed")
		}
		return err
	}
	if target.Session.ID != addressed.ID {
		v.Target = target.Session
		s.followTarget(target.Session)
	}
	if err != nil {
		s.unlock()
		return err
	}
	if held != "" {
		s.appendDelegationEvent(a.MessageID, target.Session, receipt, "held")
		v.Message = held
		return s.commit()
	}
	if woke {
		v.Message = "Woke " + addressed.Name + " and sent the request."
	}
	s.appendDelegationEvent(a.MessageID, target.Session, receipt, "dispatched")
	return s.commit()
}

// A request sent before the follower subscribes would lose the start of its reply.
func (s *Service) readyFollower(ctx context.Context, targetID string) (*follower, error) {
	s.mu.Lock()
	f := s.follow
	s.unlock()
	if f == nil || f.target.ID != targetID {
		return nil, errors.New("voice is not following the selected session's replies")
	}
	select {
	case <-f.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, fmt.Errorf("voice cannot follow the selected session: %w", f.err)
	}
	return f, nil
}

func heldReason(target *Target, addressed surface.Session, receipt *delivery.Receipt) string {
	switch {
	case (receipt.Evidence == surface.EvidenceDelivered || receipt.Evidence == surface.EvidenceTransportAccepted) &&
		(target.Session.ID != addressed.ID || surface.RouteFor(target.Adapter, target.Session, surface.AuthorPerson).TransportOnly()):
		return "Sent to " + addressed.Name + ". Voice cannot follow this session's reply; read it in the session transcript."
	case receipt.Evidence != surface.EvidenceDelivered:
		return "Target is " + string(receipt.Evidence) + "; no uncorrelated reply will be spoken."
	case receipt.TurnID == "" || receipt.TurnID == target.Session.ID:
		return "Target did not provide an authoritative turn ID; no uncorrelated reply will be spoken."
	}
	return ""
}

// The caller holds s.mu.
func (s *Service) delegationAllowed(a Action, spoken bool) error {
	v := &s.state.State
	if a.AttemptID != v.AttemptID || v.Phase != "connected" {
		return errors.New("delegation requires the current connected voice call")
	}
	if spoken && v.InputMuted {
		return errSpokenWhileMuted
	}
	if strings.TrimSpace(a.Text) == "" || len(a.Text) > 16<<10 || a.MessageID == "" || len(a.MessageID) > 128 {
		return errors.New("delegation text and a unique messageId are required")
	}
	if v.Target == nil || s.target == nil {
		return errors.New("select a target session before delegating")
	}
	for _, id := range s.state.Messages {
		if id == a.MessageID {
			return errors.New("message already submitted; inspect its outcome before sending again")
		}
	}
	return nil
}

// ctx is the follower's, so a target change cancels an update not yet handed over.
func (s *Service) speakDelegation(ctx context.Context, attemptID, messageID string, target *surface.Session, turnID, text, stage string) {
	s.mu.Lock()
	v := &s.state.State
	if v.AttemptID != attemptID || v.Phase != "connected" || v.Target == nil || v.Target.ID != target.ID || v.Session == nil {
		s.unlock()
		return
	}
	session := *v.Session
	s.unlock()
	ctx, done := context.WithTimeout(ctx, s.speechTimeout)
	defer done()
	err := s.provider.Request(ctx, &session, "thread/realtime/appendText", map[string]any{"threadId": session.ID, "text": text, "role": "developer"})
	s.mu.Lock()
	if !s.sameCall(attemptID, session.ID, target.ID) {
		s.unlock()
		return
	}
	if err != nil {
		v.Message = "Correlated " + stage + " update could not be handed to realtime audio: " + err.Error()
		s.appendDelegationEvent(messageID, target, &delivery.Receipt{Evidence: surface.EvidenceFailed, SessionID: target.ID, TurnID: turnID}, stage+"-failed")
	} else {
		s.appendDelegationEvent(messageID, target, &delivery.Receipt{Evidence: surface.EvidenceDelivered, SessionID: target.ID, TurnID: turnID}, stage)
	}
	_ = s.commit()
}

func (s *Service) recordDelegationFailure(attemptID, messageID, targetID string, err error) {
	s.mu.Lock()
	if s.state.State.Session == nil || !s.sameCall(attemptID, s.state.State.Session.ID, targetID) {
		s.unlock()
		return
	}
	s.state.State.Message = "Correlated target update ended without a final spoken result: " + err.Error()
	s.appendEvent(Event{Method: "voice/delegation/failed", Params: map[string]any{"messageId": messageID, "targetId": targetID, "error": err.Error()}})
	_ = s.commit()
}

func (s *Service) appendDelegationEvent(messageID string, target *surface.Session, receipt *delivery.Receipt, stage string) {
	s.appendEvent(Event{Method: "voice/delegation/" + stage, Params: map[string]any{"messageId": messageID, "targetId": target.ID, "turnId": receipt.TurnID, "evidence": receipt.Evidence}})
}
