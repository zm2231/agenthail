package voice

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	// unattributedWait bounds how long the follower holds a turn it cannot
	// place while a delegation's turn is not known yet. After it, the turn is
	// taken as the person's own, and the case is logged.
	unattributedWait = 30 * time.Second
	// quietGap is how long the call must be silent before a reply to typed
	// input is spoken.
	quietGap = 2 * time.Second
	// speechStale is how long a transcript delta without its done counts as
	// speech, so one lost event cannot hold replies forever.
	speechStale = 4 * time.Second
	// typedNoteBytes and typedReplyBytes bound what voice is told about a
	// turn the person typed.
	typedNoteBytes  = 300
	typedReplyBytes = 300
	// queuedRepliesMax bounds the replies waiting for a pause.
	queuedRepliesMax = 8
)

// follower follows the selected session's journal for one call, from when
// voice moves to the session until it moves away or the call ends. It speaks
// the turns voice started, notes the turns the person typed, queues their
// replies for the next pause, and refreshes the brief after a compaction.
type follower struct {
	attemptID string
	target    surface.Session
	cancel    context.CancelFunc
	// ready is closed once the subscription is prepared or failed; err is
	// why it failed.
	ready chan struct{}
	err   error
	// voice maps a turn voice started to the messages that started it.
	voice map[string][]string
	// pending counts delegations sent whose turn is not known yet, and
	// settled is closed and replaced each time one settles.
	pending int
	settled chan struct{}
	// unplaced holds the text of delegations whose route gave no turn, so
	// their turn is not taken for the person's own.
	unplaced []string
	typed    map[string]*typedTurn
}

// typedTurn is a turn voice did not start.
type typedTurn struct {
	prompt string
	reply  string
	voice  bool
}

// queuedReply is a reply to typed input waiting for a pause in the call.
type queuedReply struct {
	attemptID, targetID, text string
}

// followTarget starts following target for the current call, replacing any
// earlier follower. The caller holds s.mu.
func (s *Service) followTarget(target *surface.Session) {
	s.stopFollowing()
	if target == nil || s.stream == nil || !s.active() {
		return
	}
	ctx, cancel := s.callContext(s.background)
	f := &follower{attemptID: s.state.State.AttemptID, target: *target, cancel: cancel, ready: make(chan struct{}), voice: map[string][]string{}, settled: make(chan struct{}), typed: map[string]*typedTurn{}}
	if !s.spawn(func() { s.runFollower(ctx, f) }) {
		cancel()
		return
	}
	s.follow = f
}

// stopFollowing ends the current follower and drops replies queued for its
// session. The caller holds s.mu.
func (s *Service) stopFollowing() {
	if s.follow != nil {
		s.follow.cancel()
		s.follow = nil
	}
	s.replies = nil
}

func (s *Service) runFollower(ctx context.Context, f *follower) {
	defer f.cancel()
	subscription, err := s.stream.PrepareSessionStream(ctx, &f.target)
	s.mu.Lock()
	f.err = err
	close(f.ready)
	if err != nil {
		if s.follow == f {
			s.state.State.Message = "Voice cannot follow " + f.target.Name + ": " + err.Error()
			s.appendEvent(Event{Method: "voice/target/follow-failed", Params: map[string]any{"targetId": f.target.ID, "error": err.Error()}})
			_ = s.commit()
		} else {
			s.unlock()
		}
		return
	}
	s.unlock()
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
			continue
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
		if typed != nil {
			s.followTyped(f, event, typed)
		} else if event.SpokenAssistantContent() {
			delta := event.Body
			if strings.HasPrefix(event.Body, previous) {
				delta = strings.TrimPrefix(event.Body, previous)
			}
			if strings.TrimSpace(delta) != "" {
				stage := "interim"
				if event.Final {
					stage = "final"
				}
				s.speakDelegation(f.attemptID, messages[0], &f.target, event.TurnID, delta, stage)
			}
		}
		if event.Terminal() {
			if typed == nil && event.Failed() {
				for _, messageID := range messages {
					s.recordDelegationFailure(f.attemptID, messageID, f.target.ID, errors.New(event.FailureReason()))
				}
			}
			s.endTurn(f, event.TurnID)
			s.refreshBrief(ctx, f)
		}
	}
}

// place says whose turn an event belongs to: the messages voice sent that
// started it, or else the typed turn it is part of. A turn it cannot place
// while a delegation's turn is not known waits for that delegation to settle,
// at most unattributedWait. It reports false once the follower ends.
func (s *Service) place(ctx context.Context, f *follower, event sessionstream.Event) ([]string, *typedTurn, bool) {
	var deadline time.Time
	var ticks <-chan time.Time
	for {
		s.mu.Lock()
		if messages, ok := f.voice[event.TurnID]; ok {
			s.unlock()
			return messages, nil, true
		}
		if turn, ok := f.typed[event.TurnID]; ok {
			s.unlock()
			return nil, turn, true
		}
		if f.pending == 0 || (!deadline.IsZero() && !s.clock.now().Before(deadline)) {
			if f.pending > 0 && s.follow == f {
				if logf := s.logf; logf != nil {
					logf("voice: turn %s of %s taken as typed after waiting %s for %d delegation(s) to settle", event.TurnID, f.target.ID, unattributedWait, f.pending)
				}
				s.appendEvent(Event{Method: "voice/target/turn-unattributed", Params: map[string]any{"targetId": f.target.ID, "turnId": event.TurnID}})
				s.stage()
			}
			turn := &typedTurn{}
			f.typed[event.TurnID] = turn
			s.unlock()
			return nil, turn, true
		}
		if deadline.IsZero() {
			deadline = s.clock.now().Add(unattributedWait)
			var stop func()
			ticks, stop = s.clock.ticker()
			defer stop()
		}
		settled := f.settled
		s.unlock()
		select {
		case <-ctx.Done():
			return nil, nil, false
		case <-settled:
		case <-ticks:
		}
	}
}

// followTyped notes a turn the person typed and keeps its latest reply. The
// prompt is noted once; a turn whose prompt is a delegation sent without a
// turn stays silent, as voice said it would.
func (s *Service) followTyped(f *follower, event sessionstream.Event, turn *typedTurn) {
	s.mu.Lock()
	if s.follow != f || turn.voice {
		s.unlock()
		return
	}
	switch {
	case event.Role == "user" && turn.prompt == "" && strings.TrimSpace(event.Body) != "":
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
		return
	case event.SpokenAssistantContent():
		turn.reply = event.Body
	}
	if !event.Terminal() {
		s.unlock()
		return
	}
	text := ""
	switch {
	case event.Failed():
		text = "The turn the person typed in the session ended without a reply: " + clip(event.FailureReason(), typedReplyBytes) + "."
	case strings.TrimSpace(turn.reply) == "":
	case turn.prompt == "":
		text = "The session finished the turn it was working on: " + firstSentences(turn.reply, typedReplyBytes) + " The full reply is in the session."
	default:
		text = "The session replied to what the person typed there: " + firstSentences(turn.reply, typedReplyBytes) + " The full reply is in the session."
	}
	if text != "" {
		s.queueReply(queuedReply{attemptID: f.attemptID, targetID: f.target.ID, text: text})
	}
	s.unlock()
}

// note hands a note about the session to the call without waiting for a
// pause: it is context, and voice is told not to speak it.
func (s *Service) note(f *follower, session *surface.Session, text string) {
	if session == nil {
		return
	}
	s.mu.Lock()
	ctx, cancel := s.callContext(s.background)
	s.unlock()
	defer cancel()
	ctx, done := context.WithTimeout(ctx, s.speechTimeout)
	defer done()
	_ = s.provider.Request(ctx, session, "thread/realtime/appendText", map[string]any{"threadId": session.ID, "text": text, "role": "developer"})
}

// endTurn forgets a finished turn.
func (s *Service) endTurn(f *follower, turnID string) {
	s.mu.Lock()
	delete(f.voice, turnID)
	delete(f.typed, turnID)
	s.unlock()
}

// followerFailed fails the delegations the follower was speaking for.
func (s *Service) followerFailed(f *follower, err error) {
	s.mu.Lock()
	var messages []string
	for turnID, ids := range f.voice {
		messages = append(messages, ids...)
		delete(f.voice, turnID)
	}
	s.unlock()
	for _, messageID := range messages {
		s.recordDelegationFailure(f.attemptID, messageID, f.target.ID, err)
	}
}

// refreshBrief reads the session's brief again after a turn, and when its
// context compacted since the brief was given, gives voice the fresh one.
func (s *Service) refreshBrief(ctx context.Context, f *follower) {
	s.mu.Lock()
	briefer, given := s.briefer, s.state.State.Brief
	if briefer == nil || s.follow != f || given == nil || given.SessionID != f.target.ID {
		s.unlock()
		return
	}
	s.unlock()
	src := briefer.BriefSource(ctx, &f.target)
	if src.Compactions == nil {
		return
	}
	text, info := RenderBrief(src)
	s.mu.Lock()
	v := &s.state.State
	if s.follow != f || v.Brief == nil || v.Brief.SessionID != f.target.ID {
		s.unlock()
		return
	}
	compacted := v.Brief.Compactions != nil && *src.Compactions > *v.Brief.Compactions
	if !compacted && v.Brief.Compactions != nil {
		s.unlock()
		return
	}
	v.Brief = &info
	if compacted {
		s.appendEvent(Event{Method: "voice/target/compacted", Params: map[string]any{"targetId": f.target.ID, "compactions": *src.Compactions}})
	}
	session := v.Session
	s.stage()
	s.unlock()
	if err := s.flush(); err != nil || !compacted {
		return
	}
	s.note(f, session, "The session compacted its context, so details from before may be gone from its memory. Its brief, read again:\n\n"+text)
}

// queueReply queues a reply to typed input for the next pause, dropping the
// oldest past queuedRepliesMax. The caller holds s.mu.
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

// heard records speech in the call from a realtime event. The caller holds
// s.mu.
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

// quietAt is when the call next counts as paused: quietGap after the last
// speech. A transcript delta without its done holds the call no longer than
// speechStale. The caller holds s.mu.
func (s *Service) quietAt() time.Time {
	if s.listening {
		return s.lastSpeech.Add(max(quietGap, speechStale))
	}
	return s.lastSpeech.Add(quietGap)
}

// speakQueued speaks the oldest queued reply once the call is paused and no
// other reply is being spoken. The caller holds s.mu.
func (s *Service) speakQueued() {
	if s.replying || len(s.replies) == 0 || s.clock.now().Before(s.quietAt()) {
		return
	}
	next := s.replies[0]
	v := &s.state.State
	if next.attemptID != v.AttemptID || v.Phase != "connected" || v.Session == nil || v.Target == nil || v.Target.ID != next.targetID {
		s.replies = s.replies[1:]
		s.speakQueued()
		return
	}
	s.replies = s.replies[1:]
	session := *v.Session
	ctx, cancel := s.callContext(s.background)
	s.replying = s.spawn(func() {
		defer cancel()
		ctx, done := context.WithTimeout(ctx, s.speechTimeout)
		defer done()
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

// settleDelegation records how a delegation sent through follower f ended:
// voice speaks turnID for messageID, or, with no turn, the turn that text
// starts stays silent. The caller holds s.mu.
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

// firstSentences is text's opening sentences within limit bytes, or its
// start cut at a character when the first sentence is longer.
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
