package voice

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// A later subscription, as after a source error, reads the same events.
type journalStreamFixture struct {
	events          chan sessionstream.Event
	prepared        chan struct{}
	cancelled       chan struct{}
	prepare, cancel sync.Once
}

func (f *journalStreamFixture) PrepareSessionStream(context.Context, *surface.Session) (sessionstream.Subscription, error) {
	f.prepare.Do(func() { close(f.prepared) })
	return sessionstream.Subscription{Cursor: 12, Events: f.events, Cancel: func() { f.cancel.Do(func() { close(f.cancelled) }) }}, nil
}

func TestVoiceUsesPreparedJournalAndFiltersTurnWithBodyDelta(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Claude", Transport: "browser"}, turnID: "turn-a"}
	stream := &journalStreamFixture{events: make(chan sessionstream.Event, 8), prepared: make(chan struct{}), cancelled: make(chan struct{})}
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "target-a"})
	connectFixture(t, f)

	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect this"})
	select {
	case <-stream.prepared:
	case <-time.After(time.Second):
		t.Fatal("journal subscription was not prepared for the call")
	}
	stream.events <- sessionstream.Event{ItemID: "other", Version: 1, Kind: "text", Role: "assistant", TurnID: "other-turn", Body: "unrelated"}
	stream.events <- sessionstream.Event{ItemID: "reasoning", Version: 1, Kind: "reasoning", Role: "assistant", TurnID: "turn-a", Body: "internal reasoning"}
	stream.events <- sessionstream.Event{ItemID: "tool-call", Version: 1, Kind: "tool_call", Role: "assistant", TurnID: "turn-a", Body: "tool arguments"}
	stream.events <- sessionstream.Event{ItemID: "answer", Version: 1, Kind: "text", Role: "assistant", TurnID: "turn-a", Body: "answer"}
	stream.events <- sessionstream.Event{ItemID: "answer", Version: 2, Kind: "text", Role: "assistant", TurnID: "turn-a", Body: "answer final", Final: true}
	stream.events <- sessionstream.Event{ItemID: "later-answer", Version: 1, Kind: "message", Role: "assistant", TurnID: "turn-a", Body: "later"}
	stream.events <- sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "turn-a"}

	testkit.Eventually(t, "journal reply reaching the voice operator", 5*time.Second, func() bool { return len(f.provider.spoken()) >= 3 })
	if spoken := f.provider.spoken(); !reflect.DeepEqual(spoken, []string{"answer", " final", "later"}) {
		t.Fatalf("spoken=%q", spoken)
	}
	select {
	case <-stream.cancelled:
		t.Fatal("the call's journal subscription ended with the turn")
	default:
	}
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	select {
	case <-stream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("journal subscription was not released at hangup")
	}
}

func TestAJournalTurnRefusedByTheAPIFailsTheDelegationWithTheRefusal(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Claude", Transport: "browser"}, turnID: "turn-a"}
	stream := &journalStreamFixture{events: make(chan sessionstream.Event, 8), prepared: make(chan struct{}), cancelled: make(chan struct{})}
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "target-a"})
	connectFixture(t, f)
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect this"})
	select {
	case <-stream.prepared:
	case <-time.After(time.Second):
		t.Fatal("journal subscription was not prepared for the call")
	}
	refusal := "Claude is out of usage (You've hit your session limit)"
	stream.events <- sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "turn-a", Status: "failed", Body: refusal}
	testkit.Eventually(t, "the refused delegation to fail", 5*time.Second, func() bool { return hasEventWith(f.View(), "voice/delegation/failed", "error", refusal) })
}

func TestAJournalSourceErrorEndsTheSubscriptionAndVoiceFollowsAgain(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Claude", Transport: "browser"}, turnID: "turn-a"}
	stream := &journalStreamFixture{events: make(chan sessionstream.Event, 8), prepared: make(chan struct{}), cancelled: make(chan struct{})}
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "target-a"})
	connectFixture(t, f)
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect this"})
	stream.events <- sessionstream.Event{Kind: "source-error", Reason: "journal write failed"}
	select {
	case <-stream.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the failed subscription was not cancelled")
	}
	testkit.Eventually(t, "voice to follow the session again", 5*time.Second, func() bool {
		return hasEventWith(f.View(), "voice/target/refollowed", "error", "journal write failed")
	})
	stream.events <- sessionstream.Event{ItemID: "late", Version: 1, Kind: "text", Role: "assistant", TurnID: "turn-a", Body: "late reply", Final: true}
	stream.events <- sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "turn-a"}
	stream.events <- sessionstream.Event{ItemID: "typed", Version: 1, Kind: "message", Role: "user", TurnID: "turn-b", Body: "typed later"}
	testkit.Eventually(t, "the new follower to read on", 5*time.Second, func() bool { return hasEventWith(f.View(), "voice/target/typed", "turnId", "turn-b") })
	spoken := 0
	for _, text := range f.provider.spoken() {
		if strings.Contains(text, "late reply") {
			spoken++
		}
	}
	if spoken != 1 || !hasEventWith(f.View(), "voice/delegation/final", "messageId", "voice-request-a") || hasEventWith(f.View(), "voice/delegation/failed", "error", "journal write failed") {
		t.Fatalf("the recovered turn was not spoken once as its request's: %q %+v", f.provider.spoken(), f.View().Events)
	}
}
