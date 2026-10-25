package voice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// selectTarget routes voice to a session, or back to the orchestrator for an
// empty request. A transfer moves the connected call; it returns the target's
// name. The caller holds s.op.
// targetChange is who moves voice to a target and when.
type targetChange int

const (
	// selectForCall chooses the target before a call starts.
	selectForCall targetChange = iota
	// transferByPhone moves the current call from the phone's controls.
	transferByPhone
	// transferBySpeech moves the current call because the realtime model
	// called a tool, which a muted call refuses.
	transferBySpeech
)

func (s *Service) selectTarget(ctx context.Context, requested string, change targetChange) (string, error) {
	transfer, spoken := change != selectForCall, change == transferBySpeech
	s.mu.Lock()
	v := &s.state.State
	if spoken && v.InputMuted {
		s.unlock()
		return "", errInputMuted
	}
	if transfer && (v.Session == nil || !s.active()) {
		s.unlock()
		return "", errors.New("voice transfer requires the current call")
	}
	if !transfer && s.active() {
		s.unlock()
		return "", errors.New("end the current audio call before selecting a different voice target")
	}
	attemptID := v.AttemptID
	var session surface.Session
	if v.Session != nil {
		session = *v.Session
	}
	briefer := s.briefer
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	s.unlock()
	if !transfer {
		callCtx = ctx
	}
	requested = strings.TrimSpace(requested)
	var target *Target
	if requested != "" {
		if s.target == nil {
			return "", errors.New("session-bound voice targets are unavailable on this host")
		}
		var err error
		if target, err = s.target(callCtx, requested); err != nil {
			return "", err
		}
		if target == nil || target.Session == nil || target.Adapter == nil {
			return "", errors.New("target resolution returned no writable session")
		}
		if target.Session.Surface != surface.KindCodex && target.Session.Surface != surface.KindClaude {
			return "", errors.New("voice targets must be existing Codex or Claude sessions")
		}
		if err := surface.EnsureWritableSession(callCtx, target.Adapter, target.Session); err != nil {
			return "", err
		}
	}
	if spoken {
		if err := s.refuseMuted(); err != nil {
			return "", err
		}
	}
	var briefInfo *BriefInfo
	var outcomes map[string]TurnOutcome
	if transfer {
		text := "Voice has returned to the Agenthail orchestrator. Resume the normal Agenthail operations workflow and respond directly to the user."
		if target != nil {
			text = "Voice has transferred to the selected Agenthail session. Do not do the work yourself. The host will deliver completed user requests to that session and provide its correlated updates as developer messages. Speak those supplied updates concisely."
			var brief string
			if brief, briefInfo, outcomes = s.briefFor(callCtx, briefer, target.Session); brief != "" {
				text += "\n\n" + brief
			}
		}
		if err := s.provider.Request(callCtx, &session, "thread/realtime/appendText", map[string]any{"threadId": session.ID, "text": text, "role": "developer"}); err != nil {
			if target == nil {
				return "", fmt.Errorf("return voice to orchestrator: %w", err)
			}
			return "", fmt.Errorf("transfer voice to selected session: %w", err)
		}
	}
	s.mu.Lock()
	if transfer && (v.AttemptID != attemptID || !s.active() || v.Session == nil || v.Session.ID != session.ID) {
		s.unlock()
		return "", errors.New("the call ended before voice moved")
	}
	if !transfer && s.active() {
		s.unlock()
		return "", errors.New("end the current audio call before selecting a different voice target")
	}
	v.AudioProvider = "openai-realtime-via-codex"
	if target == nil {
		v.Target, v.CodingProvider, v.Brief = nil, "", nil
		v.Message = "Voice is with the Agenthail orchestrator."
		s.stopFollowing()
		if transfer {
			s.reported(nil, nil)
		}
		return "", s.commit()
	}
	v.Target = target.Session
	if transfer {
		v.Brief = briefInfo
		s.followTarget(target.Session, s.reported(target.Session, outcomes))
	}
	v.CodingProvider = string(target.Adapter.Name())
	if transfer {
		v.Message = "Voice transferred to " + target.Session.Name + "."
	} else {
		v.Message = "Selected " + target.Session.Name + " for this voice call."
	}
	return target.Session.Name, s.commit()
}

func (s *Service) interruptTarget(ctx context.Context, a Action) error {
	s.mu.Lock()
	v := &s.state.State
	if a.AttemptID != v.AttemptID || v.Phase != "connected" || v.Target == nil || s.target == nil {
		s.unlock()
		return errors.New("select a connected target call before interrupting it")
	}
	targetID, sessionID := v.Target.ID, v.Session.ID
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	s.unlock()
	target, err := s.target(callCtx, targetID)
	if err != nil {
		return err
	}
	if target == nil || target.Session == nil || target.Adapter == nil || target.Session.ID != targetID {
		return errors.New("selected target is no longer available")
	}
	if err := surface.EnsureWritableSession(callCtx, target.Adapter, target.Session); err != nil {
		return err
	}
	capabilities := surface.EffectiveCapabilities(target.Adapter, target.Session)
	if capabilities.ReadOnly {
		return errors.New(capabilities.ReadOnlyReason)
	}
	if !capabilities.Interrupt {
		return errors.New("selected target does not support interruption")
	}
	observation, err := target.Adapter.Observe(callCtx, target.Session)
	if err != nil {
		return fmt.Errorf("confirm selected target turn: %w", err)
	}
	if observation == nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID == "" {
		return errors.New("selected target has no confirmed active turn to interrupt")
	}
	interrupter, ok := target.Adapter.(surface.TurnInterrupter)
	if !ok {
		return errors.New("selected target cannot confirm turn-specific interruption")
	}
	if err := interrupter.InterruptTurn(callCtx, target.Session, observation.ActiveTurnID); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.sameCall(a.AttemptID, sessionID, targetID) {
		s.unlock()
		return nil
	}
	v.Message = "Interrupt requested for " + target.Session.Name + " turn " + observation.ActiveTurnID + "."
	v.Events = append(v.Events, Event{Method: "voice/target/interrupt", Params: map[string]any{"targetId": target.Session.ID, "turnId": observation.ActiveTurnID}})
	return s.commit()
}
