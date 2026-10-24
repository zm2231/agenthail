package voice

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	typedNoteBytes  = 300
	typedReplyBytes = 300
)

type typedTurn struct {
	prompt string
	reply  string
	voice  bool
}

func (s *Service) followTyped(f *follower, event sessionstream.Event, turn *typedTurn) {
	s.mu.Lock()
	if s.follow != f || turn.voice {
		s.unlock()
		return
	}
	switch {
	case event.Role == "user" && turn.prompt == "" && strings.TrimSpace(event.Body) != "":
		s.noteTyped(f, event, turn)
		return
	case event.SpokenAssistantContent():
		turn.reply = event.Body
	}
	if event.Terminal() {
		if text := typedReply(event, turn); text != "" {
			s.queueReply(queuedReply{f: f, attemptID: f.attemptID, targetID: f.target.ID, text: text})
		}
	}
	s.unlock()
}

// The caller holds s.mu; noteTyped releases it.
func (s *Service) noteTyped(f *follower, event sessionstream.Event, turn *typedTurn) {
	body := strings.TrimSpace(event.Body)
	for i, text := range f.unplaced {
		if strings.TrimSpace(text) == body {
			f.unplaced = append(f.unplaced[:i], f.unplaced[i+1:]...)
			turn.voice = true
			s.unlock()
			return
		}
	}
	turn.prompt = clip(body, typedNoteBytes)
	s.appendEvent(Event{Method: "voice/target/typed", Params: map[string]any{"targetId": f.target.ID, "turnId": event.TurnID}})
	session := s.state.State.Session
	s.stage()
	s.unlock()
	if err := s.flush(); err != nil {
		return
	}
	s.note(f, session, `The person typed in the session, not by voice: "`+turn.prompt+`". The session is working on it. Don't respond to this note; its reply will follow.`)
}

func typedReply(event sessionstream.Event, turn *typedTurn) string {
	switch {
	case event.Failed():
		return "The turn the person typed in the session ended without a reply: " + clip(event.FailureReason(), typedReplyBytes) + "."
	case strings.TrimSpace(turn.reply) == "":
		return ""
	case turn.prompt == "":
		return "The session finished the turn it was working on: " + firstSentences(turn.reply, typedReplyBytes) + " The full reply is in the session."
	default:
		return "The session replied to what the person typed there: " + firstSentences(turn.reply, typedReplyBytes) + " The full reply is in the session."
	}
}

// A note is context, not an update, so it does not wait for a pause.
func (s *Service) note(f *follower, session *surface.Session, text string) {
	if session == nil {
		return
	}
	ctx, done := context.WithTimeout(f.ctx, s.speechTimeout)
	defer done()
	_ = s.provider.Request(ctx, session, "thread/realtime/appendText", map[string]any{"threadId": session.ID, "text": text, "role": "developer"})
}

func firstSentences(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	end := 0
	for i := 0; i < len(text) && i < limit; i++ {
		if (text[i] == '.' || text[i] == '!' || text[i] == '?') && (i+1 == len(text) || text[i+1] == ' ') {
			end = i + 1
		}
	}
	if end > 0 {
		return text[:end]
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + " …"
}
