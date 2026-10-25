package voice

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// callContext is ctx, also ended when the current call ends. The caller holds
// s.mu.
func (s *Service) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	call := s.call
	if call == nil {
		call = s.background
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(call, cancel)
	return ctx, func() { stop(); cancel() }
}

// endCall cancels the work of the current call. The caller holds s.mu.
func (s *Service) endCall() {
	if s.cancelCall != nil {
		s.cancelCall()
	}
	s.stopFollowing()
	s.listening, s.lastSpeech = false, time.Time{}
}

func (s *Service) start(ctx context.Context, owner string, a Action) error {
	s.mu.Lock()
	v := &s.state.State
	if v.Session == nil {
		s.unlock()
		return errors.New("prepare the operator first")
	}
	if a.AttemptID == "" || len(a.AttemptID) > 128 || !strings.HasPrefix(a.SDP, "v=0") || len(a.SDP) > 96<<10 {
		s.unlock()
		return errors.New("a unique attemptId and audio SDP offer are required")
	}
	if a.AttemptID == v.AttemptID {
		s.unlock()
		return nil
	}
	if s.active() {
		s.unlock()
		return errors.New("end the previous call before starting a new audio connection")
	}
	session := *v.Session
	var target *surface.Session
	if v.Target != nil {
		selected := *v.Target
		target = &selected
	}
	briefer := s.briefer
	s.unlock()
	brief, briefInfo, outcomes := s.briefFor(ctx, briefer, target)
	cursor, err := s.provider.Cursor(ctx, &session)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed || s.active() || v.Session == nil || v.Session.ID != session.ID {
		s.release(session.ID, cursor)
		s.unlock()
		return errors.New("the voice operator changed while the call was starting; start it again")
	}
	*v.Session = session
	s.endCall()
	s.call, s.cancelCall = context.WithCancel(s.background)
	s.connected = make(chan struct{})
	s.cursor = cursor
	s.wakePoll()
	s.boundAttempt, s.eventGap, s.disconnectedAt = "", false, time.Time{}
	s.state.Owner = owner
	s.state.Messages = nil
	s.spoken, s.kept = nil, nil
	v.Events, v.Truncated = []Event{}, false
	v.AttemptID, v.Phase, v.SDP, v.Message, v.TerminalReason = a.AttemptID, "starting", "", "", ""
	v.InputMuted, v.InputRevision = false, 0
	s.state.NativeHeartbeatEnrolled = false
	v.AudioProvider = "openai-realtime-via-codex"
	sessionBound := v.Target != nil
	v.Brief = briefInfo
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	if err := s.commit(); err != nil {
		s.mu.Lock()
		s.release(session.ID, cursor)
		s.unlock()
		return err
	}
	err = s.provider.Request(callCtx, &session, "thread/realtime/start", StartParams(session.ID, a.AttemptID, a.SDP, sessionBound, brief))
	s.mu.Lock()
	if v.AttemptID != a.AttemptID {
		s.unlock()
		return err
	}
	var rejected *RejectedError
	switch {
	case v.Phase != "starting":
	case errors.As(err, &rejected):
		v.Phase, v.TerminalReason = "ended", "start_rejected"
		v.Message = "Voice could not start: " + rejected.Reason
		s.endCall()
		s.release(session.ID, cursor)
	case err != nil:
		v.Phase = "unknown"
		v.Message = "Call startup outcome is unknown: " + err.Error()
	}
	if v.Phase != "ended" {
		s.observe()
		s.followTarget(v.Target, s.reported(v.Target, outcomes))
	}
	if saveErr := s.commit(); saveErr != nil {
		return saveErr
	}
	return err
}

// hangup stops the call's audio. It never waits behind another action: it
// cancels the call's work in flight instead.
func (s *Service) hangup(ctx context.Context, attemptID string) error {
	s.mu.Lock()
	v := &s.state.State
	if attemptID != v.AttemptID || v.Session == nil {
		s.unlock()
		return errors.New("call identity does not match")
	}
	if !s.active() {
		s.unlock()
		return nil
	}
	session, needCursor := *v.Session, s.cursor.Source == ""
	s.unlock()
	var cursor Cursor
	if needCursor {
		var err error
		if cursor, err = s.provider.Cursor(ctx, &session); err != nil {
			return err
		}
	}
	s.mu.Lock()
	if attemptID != v.AttemptID || !s.active() {
		if needCursor {
			s.release(session.ID, cursor)
		}
		s.unlock()
		return nil
	}
	if needCursor && s.cursor.Source == "" {
		*v.Session, s.cursor = session, cursor
		s.wakePoll()
	}
	if v.TerminalReason == "" {
		v.TerminalReason = "local_hangup"
	}
	v.Phase, v.SDP = "stopping", ""
	s.endCall()
	s.stage()
	s.unlock()
	saveErr := s.flush()
	err := s.provider.Request(ctx, &session, "thread/realtime/stop", map[string]any{"threadId": session.ID})
	s.mu.Lock()
	if err != nil && attemptID == v.AttemptID && v.Phase == "stopping" {
		v.Phase = "unknown"
		v.Message = "Could not confirm hangup: " + err.Error()
	}
	if attemptID == v.AttemptID {
		s.observe()
	}
	if saveErr != nil {
		s.loadErr = saveErr
	}
	s.stage()
	s.unlock()
	if err := s.flush(); err != nil && saveErr == nil {
		saveErr = err
	}
	return errors.Join(err, saveErr)
}

// sameCall reports whether the connected call is still the one an action did
// its I/O for: the same attempt and operator, and, when targetID is given, the
// same selected target. The caller holds s.mu.
func (s *Service) sameCall(attemptID, sessionID, targetID string) bool {
	v := &s.state.State
	if v.AttemptID != attemptID || v.Phase != "connected" || v.Session == nil || v.Session.ID != sessionID {
		return false
	}
	return targetID == "" || (v.Target != nil && v.Target.ID == targetID)
}
