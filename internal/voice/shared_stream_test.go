package voice

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type journalStreamFixture struct {
	events    chan sessionstream.Event
	prepared  chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func (f *journalStreamFixture) PrepareSessionStream(context.Context, *surface.Session) (sessionstream.Subscription, error) {
	close(f.prepared)
	return sessionstream.Subscription{Cursor: 12, Events: f.events, Cancel: func() { f.once.Do(func() { close(f.cancelled) }) }}, nil
}

func TestVoiceUsesPreparedJournalAndFiltersTurnWithBodyDelta(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Claude", Transport: "browser"}, turnID: "turn-a"}
	stream := &journalStreamFixture{events: make(chan sessionstream.Event, 8), prepared: make(chan struct{}), cancelled: make(chan struct{})}
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "target-a"})
	connectFixture(t, f)

	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect this"})
	select {
	case <-stream.prepared:
	case <-time.After(time.Second):
		t.Fatal("journal subscription was not prepared before dispatch")
	}
	stream.events <- sessionstream.Event{ItemID: "other", Version: 1, Kind: "text", Role: "assistant", TurnID: "other-turn", Body: "unrelated"}
	stream.events <- sessionstream.Event{ItemID: "reasoning", Version: 1, Kind: "reasoning", Role: "assistant", TurnID: "turn-a", Body: "internal reasoning"}
	stream.events <- sessionstream.Event{ItemID: "tool-call", Version: 1, Kind: "tool_call", Role: "assistant", TurnID: "turn-a", Body: "tool arguments"}
	stream.events <- sessionstream.Event{ItemID: "answer", Version: 1, Kind: "text", Role: "assistant", TurnID: "turn-a", Body: "answer"}
	stream.events <- sessionstream.Event{ItemID: "answer", Version: 2, Kind: "text", Role: "assistant", TurnID: "turn-a", Body: "answer final", Final: true}
	stream.events <- sessionstream.Event{ItemID: "later-answer", Version: 1, Kind: "message", Role: "assistant", TurnID: "turn-a", Body: "later"}
	stream.events <- sessionstream.Event{ItemID: "done", Version: 1, Kind: "done", TurnID: "turn-a"}

	waitFor(t, "journal reply reaching the voice operator", func() bool { return len(f.provider.spoken()) >= 3 })
	if spoken := f.provider.spoken(); !reflect.DeepEqual(spoken, []string{"answer", " final", "later"}) {
		t.Fatalf("spoken=%q", spoken)
	}
	select {
	case <-stream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("journal subscription was not released")
	}
}
