package voice

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
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
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Claude", Transport: "browser"}, turnID: "turn-a"}
	stream := &journalStreamFixture{events: make(chan sessionstream.Event, 8), prepared: make(chan struct{}), cancelled: make(chan struct{})}
	s := NewWithTargetsAndOperatorSourceAndStream(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return &Target{Session: resolved, Adapter: target}, nil
	}, delivery.Dispatcher{}, nil, stream)
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "select", TargetID: "target-a"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})

	apply(t, s, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect this"})
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

	deadline := time.After(time.Second)
	for {
		p.mu.Lock()
		count := 0
		for _, params := range p.params {
			if text, ok := params["text"].(string); ok && (text == "answer" || text == " final" || text == "later") {
				count++
			}
		}
		p.mu.Unlock()
		if count >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("journal reply did not reach voice operator")
		case <-time.After(10 * time.Millisecond):
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var spoken []string
	for _, params := range p.params {
		if text, ok := params["text"].(string); ok {
			if text == "internal reasoning" || text == "tool arguments" || text == "unrelated" {
				t.Fatalf("non-spoken journal content reached voice: %q", text)
			}
			if text == "answer" || text == " final" || text == "later" {
				spoken = append(spoken, text)
			}
		}
	}
	if len(spoken) != 3 || spoken[0] != "answer" || spoken[1] != " final" || spoken[2] != "later" {
		t.Fatalf("spoken=%v", spoken)
	}
	select {
	case <-stream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("journal subscription was not released")
	}
}
