package voice

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type gatedProjects struct{ gate *gate }

func (p gatedProjects) List(ctx context.Context) ([]Project, error) {
	return nil, p.gate.wait(ctx, "List")
}
func (gatedProjects) LeadSession(context.Context, string) (string, error) {
	return "", errors.New("no lead")
}
func (gatedProjects) SendToLead(context.Context, string, string, string) (LeadReceipt, error) {
	return LeadReceipt{}, errors.New("no lead")
}

// within fails the test unless call returns inside limit.
func within(t *testing.T, what string, limit time.Duration, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { call(); close(done) }()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("%s waited behind held I/O", what)
	}
}

func interruptibleTarget() *targetFixture {
	return &targetFixture{
		session:      surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"},
		capabilities: surface.Capabilities{Send: true, Stream: true, Interrupt: true},
		observation:  &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-a"},
	}
}

func TestNoHeldCallMakesAHeartbeatAHangupOrAViewWait(t *testing.T) {
	sdp := "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"
	for _, test := range []struct {
		name string
		// held names the fixture call that blocks.
		held string
		// call says whether the held work belongs to a connected call, which
		// a hangup must cancel.
		call  bool
		setup func(*testing.T, *voiceFixture, *targetFixture)
		run   func(*voiceFixture) error
	}{
		{name: "operator creation", held: "Create", run: func(f *voiceFixture) error { _, err := f.Apply(Action{Action: "prepare"}); return err }},
		{name: "operator registration", held: "Register", setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			apply(t, f, Action{Action: "prepare"})
		}, run: func(f *voiceFixture) error { _, err := f.Apply(Action{Action: "prepare"}); return err }},
		{name: "call cursor", held: "Cursor", setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			apply(t, f, Action{Action: "prepare"})
		}, run: func(f *voiceFixture) error {
			_, err := f.Apply(Action{Action: "start", AttemptID: "call-a", SDP: sdp})
			return err
		}},
		{name: "call start", held: "thread/realtime/start", call: true, setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			apply(t, f, Action{Action: "prepare"})
		}, run: func(f *voiceFixture) error {
			_, err := f.Apply(Action{Action: "start", AttemptID: "call-a", SDP: sdp})
			return err
		}},
		{name: "text", held: "thread/realtime/appendText", call: true, setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			connectFixture(t, f)
		}, run: func(f *voiceFixture) error {
			_, err := f.Apply(Action{Action: "text", AttemptID: "call-a", MessageID: "m", Text: "hello"})
			return err
		}},
		{name: "operator interrupt", held: "Interrupt", setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			connectFixture(t, f)
		}, run: func(f *voiceFixture) error { _, err := f.Apply(Action{Action: "interrupt"}); return err }},
		{name: "target selection", held: "Resolve", setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			apply(t, f, Action{Action: "prepare"})
		}, run: func(f *voiceFixture) error { _, err := f.Apply(Action{Action: "select", TargetID: "target-a"}); return err }},
		{name: "transfer", held: "Resolve", call: true, setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			connectFixture(t, f)
		}, run: func(f *voiceFixture) error { _, err := f.Apply(Action{Action: "transfer", TargetID: "target-a"}); return err }},
		{name: "delegation delivery", held: "Send", call: true, setup: func(t *testing.T, f *voiceFixture, target *targetFixture) {
			selectAndConnect(t, f, target)
		}, run: func(f *voiceFixture) error {
			_, err := f.Apply(Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Do work"})
			return err
		}},
		{name: "spoken delegation", held: "Send", call: true, setup: func(t *testing.T, f *voiceFixture, target *targetFixture) {
			selectAndConnect(t, f, target)
		}, run: func(f *voiceFixture) error {
			f.observe(Event{Sequence: 9, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Do work"}})
			return nil
		}},
		{name: "target interrupt", held: "Observe", call: true, setup: func(t *testing.T, f *voiceFixture, target *targetFixture) {
			selectAndConnect(t, f, target)
		}, run: func(f *voiceFixture) error {
			_, err := f.Apply(Action{Action: "target-interrupt", AttemptID: "call-a"})
			return err
		}},
		{name: "tool call", held: "List", call: true, setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			f.service.SetProjects(gatedProjects{gate: f.gate})
			connectFixture(t, f)
		}, run: func(f *voiceFixture) error {
			f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "tool-a", "threadId": "operator", "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}}})
			return nil
		}},
		{name: "delegated speech", held: "thread/realtime/appendText", call: true, setup: func(t *testing.T, f *voiceFixture, target *targetFixture) {
			target.perTurn = true
			selectAndConnect(t, f, target)
			apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "m", Text: "Do work"})
		}, run: func(f *voiceFixture) error {
			f.target.reply("turn-1", "Done.")
			return nil
		}},
		{name: "event poll", held: "Poll", setup: func(t *testing.T, f *voiceFixture, _ *targetFixture) {
			connectFixture(t, f)
		}, run: func(f *voiceFixture) error {
			f.ticks <- time.Time{}
			return nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := interruptibleTarget()
			var f *voiceFixture
			f = newFixture(t, fixtureOptions{target: target, register: func(ctx context.Context, _ surface.Session) error {
				return f.gate.wait(ctx, "Register")
			}})
			f.target = target
			if test.setup != nil {
				test.setup(t, f, target)
			}
			f.gate.hold(test.held)
			defer f.gate.release(test.held)
			result := make(chan error, 1)
			go func() { result <- test.run(f) }()
			f.gate.reached(t, test.held)
			within(t, "View", 100*time.Millisecond, func() { f.View() })
			within(t, "a heartbeat", 100*time.Millisecond, func() { _, _ = f.Apply(Action{Action: "heartbeat", AttemptID: "call-a"}) })
			if !test.call {
				return
			}
			// A hangup writes the state file itself, so it gets longer.
			within(t, "a hangup", time.Second, func() {
				if _, err := f.Apply(Action{Action: "stop", AttemptID: "call-a"}); err != nil {
					t.Errorf("hangup: %v", err)
				}
			})
			waitFor(t, "the hangup to cancel the held call", func() bool { return f.gate.waiters() == 0 })
			select {
			case <-result:
			case <-time.After(2 * time.Second):
				t.Fatal("the held call kept running after the hangup")
			}
		})
	}
}

func selectAndConnect(t *testing.T, f *voiceFixture, target *targetFixture) {
	t.Helper()
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)
}

func TestOverlappingDelegationsEachSpeakTheirOwnReply(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"}, perTurn: true}
	f := newFixture(t, fixtureOptions{target: target})
	f.target = target
	selectAndConnect(t, f, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "first", Text: "First request"})
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "second", Text: "Second request"})
	target.reply("turn-2", "Second reply.")
	target.reply("turn-1", "First reply.")
	waitFor(t, "each delegation to finish with its own turn", func() bool {
		view := f.View()
		return hasDelegation(view, "voice/delegation/final", "first", "turn-1") && hasDelegation(view, "voice/delegation/final", "second", "turn-2")
	})
	for _, event := range f.View().Events {
		if event.Method == "voice/delegation/final" && event.Params["turnId"] != map[string]string{"first": "turn-1", "second": "turn-2"}[event.Params["messageId"].(string)] {
			t.Fatalf("a reply was attributed to another request: %+v", event)
		}
	}
	spoken := strings.Join(f.provider.spoken(), "|")
	if !strings.Contains(spoken, "First reply.") || !strings.Contains(spoken, "Second reply.") {
		t.Fatalf("spoken=%q", spoken)
	}
}

func hasDelegation(v State, method, messageID, turnID string) bool {
	for _, event := range v.Events {
		if event.Method == method && event.Params["messageId"] == messageID && event.Params["turnId"] == turnID {
			return true
		}
	}
	return false
}

func TestSpokenRequestsQueuedForAnEndedCallAreDroppedWithTheReason(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target})
	selectAndConnect(t, f, target)
	f.gate.hold("Send")
	defer f.gate.release("Send")
	f.observe(
		Event{Sequence: 1, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "First"}},
		Event{Sequence: 2, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Second"}},
	)
	f.gate.reached(t, "Send")
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	waitFor(t, "the queued request to be dropped", func() bool {
		for _, event := range f.View().Events {
			if event.Method == "voice/delegation/held" && event.Params["messageId"] == "voice-transcript:2" {
				return strings.Contains(event.Params["error"].(string), "the call ended before this request was delivered")
			}
		}
		return false
	})
	if sends := target.sends(); len(sends) != 0 {
		t.Fatalf("an ended call's request was delivered: %v", sends)
	}
}

func TestAToolCallAfterCloseIsAnsweredThatVoiceIsShuttingDown(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	f.service.mu.Lock()
	f.service.closed = true
	f.service.unlock()
	f.service.runTool(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "tool-a", "threadId": "operator", "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}}})
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.toolCalls) != 1 || p.toolCalls[0].success || !strings.Contains(p.toolCalls[0].text, "shutting down") {
		t.Fatalf("toolCalls=%+v", p.toolCalls)
	}
}
