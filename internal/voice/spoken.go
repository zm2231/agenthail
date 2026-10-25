package voice

import (
	"errors"
	"strconv"
	"strings"
)

// errInputMuted answers a voice tool that would act while the phone's
// microphone is muted: speech the person did not mean to send must not act.
// Reading tools stay available.
var errInputMuted = errors.New("Microphone input is muted. No voice-requested action was taken; unmute or use the call controls.")

var errSpokenWhileMuted = errors.New("microphone input was muted; the request was not sent")

// refuseMuted returns errInputMuted while the call's input is muted. Actions
// taken by voice check it again just before their irreversible I/O, since a
// mute can arrive while they are planned. The caller holds no lock.
func (s *Service) refuseMuted() error {
	s.mu.Lock()
	muted := s.state.State.InputMuted
	s.unlock()
	if muted {
		return errInputMuted
	}
	return nil
}

type spokenRequest struct {
	attemptID, messageID, text string
}

// queueSpokenTranscript queues speech-to-text from the realtime audio
// provider for the selected coding session, in the order it was spoken.
// Audible target updates still require the delivery receipt and target turn
// ID returned by that session. The caller holds s.mu.
func (s *Service) queueSpokenTranscript(e Event) {
	if e.Method != "thread/realtime/transcript/done" || s.state.State.Target == nil {
		return
	}
	role, _ := e.Params["role"].(string)
	text, _ := e.Params["text"].(string)
	text = strings.TrimSpace(text)
	if role != "user" || text == "" {
		return
	}
	messageID := "voice-transcript:" + strconv.FormatInt(e.Sequence, 10)
	if s.kept[messageID] || s.spokenInFlight == messageID {
		return
	}
	for _, id := range s.state.Messages {
		if id == messageID {
			return
		}
	}
	for _, queued := range s.spoken {
		if queued.messageID == messageID {
			return
		}
	}
	if s.state.State.InputMuted {
		s.keep(messageID)
		s.holdSpoken(messageID, errSpokenWhileMuted)
		return
	}
	if voiceOnly(text) {
		s.keep(messageID)
		s.holdVoiceOnly(messageID, text)
		return
	}
	s.spoken = append(s.spoken, spokenRequest{attemptID: s.state.State.AttemptID, messageID: messageID, text: text})
	if !s.delivering {
		s.delivering = s.spawn(s.deliverSpoken)
	}
}

// deliverSpoken delivers queued spoken requests one at a time, and drops
// those whose call ended first.
func (s *Service) deliverSpoken() {
	for {
		s.mu.Lock()
		if len(s.spoken) == 0 {
			s.delivering = false
			s.unlock()
			return
		}
		next := s.spoken[0]
		s.spoken = s.spoken[1:]
		v := &s.state.State
		if next.attemptID != v.AttemptID || v.Phase != "connected" {
			s.holdSpoken(next.messageID, errors.New("the call ended before this request was delivered"))
			_ = s.commit()
			continue
		}
		s.spokenInFlight = next.messageID
		s.unlock()
		s.op.Lock()
		err := s.delegate(s.background, Action{Action: "delegate", AttemptID: next.attemptID, Text: next.text, MessageID: next.messageID}, true)
		s.op.Unlock()
		s.mu.Lock()
		s.spokenInFlight = ""
		if err == nil {
			s.unlock()
			continue
		}
		if v.AttemptID == next.attemptID {
			v.Message = "Spoken request was not delivered to the selected target: " + err.Error()
		}
		s.holdSpoken(next.messageID, err)
		_ = s.commit()
	}
}

// holdSpoken records a spoken request that was not delivered. The caller
// holds s.mu.
func (s *Service) holdSpoken(messageID string, err error) {
	params := map[string]any{"messageId": messageID, "error": err.Error()}
	if target := s.state.State.Target; target != nil {
		params["targetId"] = target.ID
	}
	s.appendEvent(Event{Method: "voice/delegation/held", Params: params})
}

// keep records that the call held a spoken transcript. The caller holds s.mu.
func (s *Service) keep(messageID string) {
	if s.kept == nil {
		s.kept = map[string]bool{}
	}
	s.kept[messageID] = true
}

// holdVoiceOnly keeps filler with voice instead of sending it to the selected
// session, and logs it so a wrong hold can be found. The caller holds s.mu.
func (s *Service) holdVoiceOnly(messageID, text string) {
	target := s.state.State.Target
	s.appendEvent(Event{Method: "voice/utterance/voice-only", Params: map[string]any{"messageId": messageID, "targetId": target.ID, "text": text}})
	if logf := s.logf; logf != nil {
		name, id := target.Name, target.ID
		s.later(func() {
			logf("voice: kept %q with voice instead of sending it to %s (%s, %s)", text, name, id, messageID)
		})
	}
}
