package voice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// targetJournal replays a targetFixture's scripted replies as journal events.
type targetJournal struct {
	mu        sync.Mutex
	target    *targetFixture
	events    chan sessionstream.Event
	done      chan struct{}
	stops     []context.CancelFunc
	prepared  int
	cancelled int
}

func (j *targetJournal) PrepareSessionStream(context.Context, *surface.Session) (sessionstream.Subscription, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	events, done := make(chan sessionstream.Event, 64), make(chan struct{})
	j.events, j.done = events, done
	j.prepared++
	var once sync.Once
	return sessionstream.Subscription{Events: events, Cancel: func() {
		once.Do(func() {
			close(done)
			j.mu.Lock()
			j.cancelled++
			stops := j.stops
			j.stops = nil
			j.mu.Unlock()
			for _, stop := range stops {
				stop()
			}
		})
	}}, nil
}

func (j *targetJournal) subscriptions() (prepared, cancelled int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.prepared, j.cancelled
}

func (j *targetJournal) emit(e sessionstream.Event) {
	j.mu.Lock()
	events, done := j.events, j.done
	j.mu.Unlock()
	if events == nil {
		return
	}
	select {
	case events <- e:
	case <-done:
	}
}

func (j *targetJournal) replay(session surface.Session, turnID, prompt string) {
	j.emit(sessionstream.Event{ItemID: "prompt-" + turnID, Version: 1, Kind: "message", Role: "user", TurnID: turnID, Body: prompt})
	ctx, stop := context.WithCancel(context.Background())
	j.mu.Lock()
	j.stops = append(j.stops, stop)
	j.mu.Unlock()
	defer stop()
	bodies, versions := map[string]string{}, map[string]uint64{}
	ended := false
	err := j.target.Stream(ctx, &session, turnID, func(e surface.StreamEvent) {
		if e.Class() == surface.StreamEventTerminal {
			ended = true
			j.emit(sessionstream.Event{ItemID: "done-" + turnID, Kind: e.Kind, TurnID: turnID, Status: e.Status, Body: e.Text})
			return
		}
		id := e.ID
		if id == "" {
			id = e.ProviderKey
		}
		if id == "" {
			id = "item-" + turnID
		}
		if e.Operation == "append" {
			bodies[id] += e.Text
		} else {
			bodies[id] = e.Text
		}
		versions[id]++
		if e.Version > 0 {
			versions[id] = e.Version
		}
		role := e.Role
		if role == "" {
			role = "assistant"
		}
		j.emit(sessionstream.Event{ItemID: id, Version: versions[id], Kind: e.Kind, Role: role, TurnID: turnID, Body: bodies[id], Final: e.Final})
	}, time.Minute)
	switch {
	case ended || ctx.Err() != nil:
	case err != nil:
		j.emit(sessionstream.Event{ItemID: "done-" + turnID, Kind: "done", TurnID: turnID, Status: "failed", Body: err.Error()})
	default:
		j.emit(sessionstream.Event{ItemID: "done-" + turnID, Kind: "done", TurnID: turnID})
	}
}

func followedTarget(t *testing.T, briefer Briefer) (*voiceFixture, *targetFixture) {
	t.Helper()
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop", Status: surface.StatusIdle}}
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	if briefer != nil {
		f.service.SetBriefer(briefer)
	}
	selectAndConnect(t, f, target)
	testkit.Eventually(t, "the call to follow its session", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 1
	})
	return f, target
}

func typeTurn(j *targetJournal, turnID, prompt, reply string) {
	j.emit(sessionstream.Event{ItemID: "prompt-" + turnID, Version: 1, Kind: "message", Role: "user", TurnID: turnID, Body: prompt})
	j.emit(sessionstream.Event{ItemID: "reply-" + turnID, Version: 1, Kind: "text", Role: "assistant", TurnID: turnID, Body: reply, Final: true})
	j.emit(sessionstream.Event{ItemID: "done-" + turnID, Version: 1, Kind: "done", TurnID: turnID})
}

func (f *voiceFixture) queuedReplies() int {
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	return len(f.service.replies)
}

func spokenWith(f *voiceFixture, text string) int {
	n := 0
	for _, spoken := range f.provider.spoken() {
		if strings.Contains(spoken, text) {
			n++
		}
	}
	return n
}

func TestATurnThePersonTypedIsNotedAndItsReplySpokenOnceAtAPause(t *testing.T) {
	f, target := followedTarget(t, nil)
	reply := "The linter passed. Two warnings remain in brief.go. " + strings.Repeat("Here is a long explanation of each warning. ", 20)
	typeTurn(target.journal, "typed-1", "Run the linter", reply)
	testkit.Eventually(t, "the typed turn's reply to be spoken", 5*time.Second, func() bool { return len(f.provider.spoken()) == 2 })
	spoken := f.provider.spoken()
	if !strings.Contains(spoken[0], `The person typed in the session, not by voice: "Run the linter".`) {
		t.Fatalf("note=%q", spoken[0])
	}
	want := "The session replied to what the person typed there: The linter passed. Two warnings remain in brief.go."
	if !strings.HasPrefix(spoken[1], want) || !strings.HasSuffix(spoken[1], "The full reply is in the session.") || len(spoken[1]) > len(want)+typedReplyBytes+len(" The full reply is in the session.") {
		t.Fatalf("reply=%q", spoken[1])
	}
	testkit.Eventually(t, "the typed turn and its spoken reply to be recorded", 5*time.Second, func() bool {
		return hasEventWith(f.View(), "voice/target/typed", "turnId", "typed-1") && hasEventWith(f.View(), "voice/target/reply-spoken", "targetId", "target-a")
	})
	typeTurn(target.journal, "typed-1", "Run the linter", reply)
	testkit.Eventually(t, "the follower to read the repeated events", 5*time.Second, func() bool {
		target.journal.mu.Lock()
		defer target.journal.mu.Unlock()
		return len(target.journal.events) == 0
	})
	if got := len(f.provider.spoken()); got != 2 {
		t.Fatalf("a repeated turn was spoken again: %q", f.provider.spoken())
	}
}

func TestATypedReplyWaitsForThePersonAndVoiceToPause(t *testing.T) {
	f, target := followedTarget(t, nil)
	f.observe(Event{Method: "thread/realtime/transcript/delta", Params: map[string]any{"role": "user", "delta": "I want"}})
	typeTurn(target.journal, "typed-1", "Run the linter", "The linter passed.")
	testkit.Eventually(t, "the reply to wait for a pause", 5*time.Second, func() bool { return f.queuedReplies() == 1 })
	f.advance(time.Second)
	f.observe(Event{Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "I want the tests"}})
	f.observe(Event{Method: "thread/realtime/transcript/delta", Params: map[string]any{"role": "assistant", "delta": "Sure"}})
	f.advance(quietGap - 100*time.Millisecond)
	f.tick()
	if f.queuedReplies() != 1 || spokenWith(f, "The linter passed.") != 0 {
		t.Fatalf("the reply interrupted the call: %q", f.provider.spoken())
	}
	f.advance(200 * time.Millisecond)
	f.tick()
	testkit.Eventually(t, "the reply to be spoken at the pause", 5*time.Second, func() bool { return spokenWith(f, "The linter passed.") == 1 })
}

func TestASpeechDeltaWithoutItsDoneHoldsATypedReplyOnlyBriefly(t *testing.T) {
	f, target := followedTarget(t, nil)
	f.observe(Event{Method: "thread/realtime/transcript/delta", Params: map[string]any{"role": "user", "delta": "I want"}})
	typeTurn(target.journal, "typed-1", "Run the linter", "The linter passed.")
	testkit.Eventually(t, "the reply to wait for a pause", 5*time.Second, func() bool { return f.queuedReplies() == 1 })
	f.advance(speechStale - 100*time.Millisecond)
	f.tick()
	if spokenWith(f, "The linter passed.") != 0 {
		t.Fatalf("the reply did not wait for the person: %q", f.provider.spoken())
	}
	f.advance(200 * time.Millisecond)
	f.tick()
	testkit.Eventually(t, "a lost done to stop holding the reply", 5*time.Second, func() bool { return spokenWith(f, "The linter passed.") == 1 })
}

func TestATurnVoiceCannotPlaceWhileADelegationIsUnsettledIsTakenAsTypedAfterItsBound(t *testing.T) {
	f, target := followedTarget(t, nil)
	logged, release := make(chan string, 1), make(chan struct{})
	f.service.SetLog(func(format string, args ...any) {
		logged <- fmt.Sprintf(format, args...)
		<-release
	})
	f.gate.hold("Send")
	sent := make(chan error, 1)
	go func() {
		_, err := f.Apply(Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Do work"})
		sent <- err
	}()
	f.gate.reached(t, "Send")
	typeTurn(target.journal, "typed-1", "Run the linter", "The linter passed.")
	testkit.Eventually(t, "the follower to wait on the delegation beside the poll", 5*time.Second, func() bool { return f.provider.waiting.Load() >= 2 })
	if spokenWith(f, "Run the linter") != 0 {
		t.Fatalf("a turn was placed while a delegation was unsettled: %q", f.provider.spoken())
	}
	f.advance(unattributedWait + time.Second)
	testkit.Eventually(t, "the unplaced turn to be taken as typed", 5*time.Second, func() bool {
		select {
		case f.ticks <- time.Time{}:
		default:
		}
		return hasEventWith(f.View(), "voice/target/turn-unattributed", "turnId", "typed-1") && spokenWith(f, "The linter passed.") == 1
	})
	select {
	case line := <-logged:
		if !strings.Contains(line, "turn typed-1 of target-a taken as typed") {
			t.Fatalf("log=%q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the unplaced turn was not logged")
	}
	whileHeld(t, f.gate, "a heartbeat while the log is slow", func() { _, _ = f.Apply(Action{Action: "heartbeat", AttemptID: "call-a"}) })
	close(release)
	f.gate.release("Send")
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

func TestMovingVoiceAwayEndsTheSessionsFollowerAndSpeaksNothingMoreFromIt(t *testing.T) {
	f, target := followedTarget(t, nil)
	apply(t, f, Action{Action: "transfer", AttemptID: "call-a", TargetID: ""})
	if _, cancelled := target.journal.subscriptions(); cancelled != 1 {
		t.Fatalf("cancelled=%d", cancelled)
	}
	typeTurn(target.journal, "typed-1", "Run the linter", "The linter passed.")
	if spoken := f.provider.spoken(); len(spoken) != 1 || !strings.HasPrefix(spoken[0], "Voice has returned to the Agenthail orchestrator.") {
		t.Fatalf("spoken=%q", spoken)
	}
}

type compactingBriefer struct {
	mu          sync.Mutex
	compactions int
}

func (b *compactingBriefer) BriefSource(_ context.Context, session *surface.Session) BriefSource {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := b.compactions
	return BriefSource{Session: *session, Compactions: &count, Exchanges: []surface.Exchange{{User: "Why?", Assistant: fmt.Sprintf("Answer after %d compactions.", count)}}}
}

func (b *compactingBriefer) compact() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.compactions++
}

func TestACompactionDuringTheCallSendsTheFreshBrief(t *testing.T) {
	briefer := &compactingBriefer{compactions: 1}
	f, target := followedTarget(t, briefer)
	typeTurn(target.journal, "typed-1", "Run the linter", "The linter passed.")
	testkit.Eventually(t, "the first turn to finish", 5*time.Second, func() bool { return spokenWith(f, "The linter passed.") == 1 })
	if spokenWith(f, "compacted") != 0 {
		t.Fatalf("a turn without a compaction sent a note: %q", f.provider.spoken())
	}
	briefer.compact()
	typeTurn(target.journal, "typed-2", "Keep going", "Done.")
	testkit.Eventually(t, "the fresh brief after the compaction", 5*time.Second, func() bool {
		return spokenWith(f, "The session compacted its context") == 1 && spokenWith(f, "Answer after 2 compactions.") == 1
	})
	if b := f.View().Brief; b == nil || b.Compactions == nil || *b.Compactions != 2 || !hasEventWith(f.View(), "voice/target/compacted", "targetId", "target-a") {
		t.Fatalf("brief=%+v", b)
	}
}

func (p *fixtureProvider) cancelledSpeech() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unspoken
}

func TestSpeechAboutASessionStillBeingHandedOverIsCancelledWhenVoiceLeavesIt(t *testing.T) {
	for _, test := range []struct {
		name, held string
		run        func(f *voiceFixture, target *targetFixture)
	}{
		{name: "delegated reply", held: "The old reply.", run: func(f *voiceFixture, target *targetFixture) {
			apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Do work"})
			target.journal.emit(sessionstream.Event{ItemID: "reply", Version: 1, Kind: "text", Role: "assistant", TurnID: "target-turn", Body: "The old reply."})
		}},
		{name: "typed reply", held: "The session replied to what the person typed there: The old reply. The full reply is in the session.", run: func(f *voiceFixture, target *targetFixture) {
			typeTurn(target.journal, "typed-1", "Run the linter", "The old reply.")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop", Status: surface.StatusIdle}, streaming: make(chan struct{})}
			f := newFixture(t, fixtureOptions{target: target})
			f.target = target
			speaking := make(chan struct{})
			f.provider.speaking, f.provider.speakingText = speaking, test.held
			selectAndConnect(t, f, target)
			testkit.Eventually(t, "the call to follow its session", 5*time.Second, func() bool {
				prepared, _ := target.journal.subscriptions()
				return prepared == 1
			})
			test.run(f, target)
			select {
			case <-speaking:
			case <-time.After(5 * time.Second):
				t.Fatal("the update never reached the operator")
			}
			apply(t, f, Action{Action: "transfer", AttemptID: "call-a", TargetID: ""})
			testkit.Eventually(t, "the held update to be cancelled with the move", 2*time.Second, func() bool { return f.provider.cancelledSpeech() == 1 })
			testkit.Eventually(t, "the old subscription to end", 2*time.Second, func() bool {
				_, cancelled := target.journal.subscriptions()
				return cancelled == 1
			})
			if spokenWith(f, "The old reply.") != 0 {
				t.Fatalf("old session text reached voice: %q", f.provider.spoken())
			}
		})
	}
}

func TestASourceErrorEndsItsFollowerAndTheNextOneLeavesItsTurnsAlone(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Fieldnotes build", Transport: "desktop", Status: surface.StatusIdle}, streaming: make(chan struct{})}
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	selectAndConnect(t, f, target)
	testkit.Eventually(t, "the call to follow its session", 5*time.Second, func() bool {
		prepared, _ := target.journal.subscriptions()
		return prepared == 1
	})
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Do work"})
	target.journal.emit(sessionstream.Event{Kind: "source-error", Reason: "transcript read failed"})
	testkit.Eventually(t, "the source error to end the delegation and its follower", 5*time.Second, func() bool {
		prepared, cancelled := target.journal.subscriptions()
		return hasEventWith(f.View(), "voice/delegation/failed", "error", "transcript read failed") && hasEventWith(f.View(), "voice/target/refollowed", "error", "transcript read failed") && prepared == 2 && cancelled == 1
	})
	target.journal.emit(sessionstream.Event{ItemID: "reply", Version: 1, Kind: "text", Role: "assistant", TurnID: "target-turn", Body: "Resumed reply.", Final: true})
	target.journal.emit(sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "target-turn", Status: "failed", Body: "late failure"})
	typeTurn(target.journal, "typed-1", "Run the linter", "The linter passed.")
	testkit.Eventually(t, "a later typed turn to be spoken", 5*time.Second, func() bool { return spokenWith(f, "The linter passed.") == 1 })
	if spokenWith(f, "Resumed reply.") != 0 || hasEventWith(f.View(), "voice/target/typed", "turnId", "target-turn") || hasEventWith(f.View(), "voice/delegation/failed", "error", "late failure") {
		t.Fatalf("a retired turn was followed again: spoken=%q events=%+v", f.provider.spoken(), f.View().Events)
	}
}
