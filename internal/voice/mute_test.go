package voice

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func muteCall(t *testing.T, f *voiceFixture, muted bool, revision int64) State {
	t.Helper()
	return apply(t, f, Action{Action: "input-state", AttemptID: "call-a", Muted: &muted, InputRevision: revision})
}

func TestAMutedCallRefusesVoiceActionsAndAnOlderReportCannotUnmuteIt(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target})
	p := f.provider
	connectFixture(t, f)
	muteCall(t, f, true, 2)
	if v := muteCall(t, f, false, 1); !v.InputMuted || v.InputRevision != 2 {
		t.Fatalf("an older report unmuted the call: %+v", v)
	}
	for _, tool := range []string{"voice_transfer", "voice_return_to_orchestrator", "voice_send_to_lead"} {
		f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": tool, "threadId": "operator", "namespace": "agenthail", "tool": tool, "arguments": map[string]any{"targetId": "target-a", "project": "p", "message": "m"}}})
	}
	testkit.Eventually(t, "the refusals", 5*time.Second, func() bool { return len(p.answers()) == 3 })
	p.mu.Lock()
	for _, call := range p.toolCalls {
		if call.success || !strings.Contains(call.text, "Microphone input is muted") {
			t.Errorf("a muted call let %s act: %+v", call.requestID, call)
		}
	}
	p.mu.Unlock()
	if got := f.View().Target; got != nil {
		t.Fatalf("a muted call transferred: %+v", got)
	}
	muteCall(t, f, false, 3)
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "after", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "target-a"}}})
	testkit.Eventually(t, "the unmuted transfer", 5*time.Second, func() bool { return len(p.answers()) == 4 })
	if got := f.View().Target; got == nil || got.ID != "target-a" {
		t.Fatalf("an unmuted call could not transfer: %+v", got)
	}
}

func TestAMutedCallStillAnswersReadingTools(t *testing.T) {
	f, p := fixture(t)
	f.service.SetProjects(gatedProjects{})
	connectFixture(t, f)
	muteCall(t, f, true, 1)
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "read", "threadId": "operator", "namespace": "agenthail", "tool": "voice_projects", "arguments": map[string]any{}}})
	testkit.Eventually(t, "the projects answer", 5*time.Second, func() bool { return len(p.answers()) == 1 })
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.toolCalls[0].success {
		t.Fatalf("a muted call refused a reading tool: %+v", p.toolCalls[0])
	}
}

func TestSpeechWhileMutedIsNotSentToTheSession(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target})
	selectAndConnect(t, f, target)
	muteCall(t, f, true, 1)
	f.observe(Event{Sequence: 4, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Ghost input"}})
	if !hasEventWith(f.View(), "voice/delegation/held", "messageId", "voice-transcript:4") {
		t.Fatalf("muted speech was not recorded as held: %+v", f.View().Events)
	}
	f.service.Close()
	if sends := target.sends(); len(sends) != 0 {
		t.Fatalf("muted speech reached the session: %v", sends)
	}
}

func TestEachCallStartsUnmuted(t *testing.T) {
	f, _ := fixture(t)
	connectFixture(t, f)
	muteCall(t, f, true, 5)
	hangUp(t, f)
	v := apply(t, f, Action{Action: "start", AttemptID: "call-b", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"})
	if v.InputMuted || v.InputRevision != 0 {
		t.Fatalf("a new call kept the last call's mute: %+v", v)
	}
	if _, err := f.Apply(Action{Action: "input-state", AttemptID: "call-a", Muted: new(bool), InputRevision: 9}); err == nil {
		t.Fatal("the last call's input state was accepted for a new call")
	}
}

func appendTexts(p *fixtureProvider) int {
	requests, _, _ := p.snapshot()
	n := 0
	for _, method := range requests {
		if method == "thread/realtime/appendText" {
			n++
		}
	}
	return n
}

func TestAMuteDuringASpokenTransferStopsItBeforeVoiceMoves(t *testing.T) {
	for _, test := range []struct {
		name, held string
		arguments  map[string]any
	}{
		{name: "to a session", held: "Resolve", arguments: map[string]any{"targetId": "target-a"}},
		{name: "to a project's coordinator", held: "LeadSession", arguments: map[string]any{"project": "target-a"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"}}
			f := newFixture(t, fixtureOptions{target: target})
			f.service.SetProjects(gatedProjects{gate: f.gate})
			p := f.provider
			connectFixture(t, f)
			before := appendTexts(p)
			f.gate.hold(test.held)
			f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "move", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": test.arguments}})
			f.gate.reached(t, test.held)
			muteCall(t, f, true, 1)
			f.gate.release(test.held)
			testkit.Eventually(t, "the refusal", 5*time.Second, func() bool { return len(p.answers()) == 1 })
			p.mu.Lock()
			call := p.toolCalls[0]
			p.mu.Unlock()
			if call.success || !strings.Contains(call.text, "Microphone input is muted") {
				t.Fatalf("a transfer planned before the mute went ahead: %+v", call)
			}
			if got := f.View().Target; got != nil {
				t.Fatalf("voice moved while muted: %+v", got)
			}
			if n := appendTexts(p); n != before {
				t.Fatalf("Codex was told voice moved: %d appendText requests, want %d", n, before)
			}
		})
	}
}

func TestAMuteDuringASpokenRequestHoldsItAndUnmutingDoesNotReplayIt(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Build session", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target})
	selectAndConnect(t, f, target)
	f.gate.hold("Resolve")
	f.observe(Event{Sequence: 7, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Ghost input"}})
	f.gate.reached(t, "Resolve")
	muteCall(t, f, true, 1)
	f.gate.release("Resolve")
	testkit.Eventually(t, "the held request", 5*time.Second, func() bool {
		return hasEventWith(f.View(), "voice/delegation/held", "messageId", "voice-transcript:7")
	})
	muteCall(t, f, false, 2)
	f.service.Close()
	if sends := target.sends(); len(sends) != 0 {
		t.Fatalf("speech the person muted reached the session: %v", sends)
	}
}
