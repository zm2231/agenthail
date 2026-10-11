package voice

import (
	"encoding/json"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"testing"
)

func TestClientOwnedDelegationResultsAreCorrelatedAndDeduplicated(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	apply(t, f, Action{Action: "prepare"})
	connectFixture(t, f)
	for index, id := range []string{"first", "follow-up", "repeated-question"} {
		raw, _ := json.Marshal(map[string]any{"type": "delegation.created", "item": map[string]any{"type": "delegation", "target": "client", "id": id, "content": []map[string]any{{"type": "input_text", "text": "Check the session"}}}})
		apply(t, f, Action{Action: "realtime-event", AttemptID: "call-a", EventSequence: uint64(index + 1), Event: raw})
		waitFor(t, "executor receipt", func() bool {
			f.service.mu.Lock()
			defer f.service.mu.Unlock()
			return f.service.delegations[id].turn != ""
		})
		pollToolFixture(t, f,
			Event{Method: "item/completed", Params: map[string]any{"turnId": "executor-turn", "item": map[string]any{"type": "agentMessage", "id": id + "-progress", "phase": "commentary", "text": "Checking"}}},
			Event{Method: "item/completed", Params: map[string]any{"turnId": "executor-turn", "item": map[string]any{"type": "agentMessage", "id": id + "-final", "phase": "final_answer", "text": "Complete"}}},
			Event{Method: "item/completed", Params: map[string]any{"turnId": "executor-turn", "item": map[string]any{"type": "agentMessage", "id": id + "-final", "phase": "final_answer", "text": "Complete"}}})
		frames := f.View().ContextFrames
		if len(frames) != 2 || frames[0].DelegationID != id || frames[1].DelegationID != id || frames[0].Channel != "speakable" {
			t.Fatalf("misrouted frames: %+v", frames)
		}
		apply(t, f, Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frames[1].ID})
		pollToolFixture(t, f, Event{Method: "turn/completed", Params: map[string]any{"turn": map[string]any{"id": "executor-turn"}}})
	}
}

func TestWorkerJournalRetainsStateWithoutEmittingVoiceFrames(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Transport: "uds"}}
	stream := newJournalFixture()
	f := boundFixture(t, target, stream)
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "status", Version: 1, Kind: "message", Role: "assistant", Body: "I will inspect this call", Final: true}
	waitFor(t, "retained worker observation", func() bool { return len(workerItems(f.service)) == 1 })
	if frames := f.View().ContextFrames; len(frames) != 0 {
		t.Fatalf("unsolicited voice output: %+v", frames)
	}
}

func TestVoiceNativeResultAcknowledgmentIsAttemptBound(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	apply(t, f, Action{Action: "prepare"})
	connectFixture(t, f)
	f.service.mu.Lock()
	d := &delegation{id: "request", attempt: "call-a", generation: f.service.targetGeneration}
	f.service.queueDelegationResult(d, "Result", true)
	f.service.unlock()
	frame := f.View().ContextFrames[0]
	if _, err := f.Apply(Action{Action: "context-ack", AttemptID: "retired", ContextAck: frame.ID}); err == nil {
		t.Fatal("retired attempt acknowledged a result")
	}
	if _, err := f.Apply(Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frame.ID + 1}); err == nil {
		t.Fatal("future result acknowledged")
	}
	apply(t, f, Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frame.ID})
	if len(f.View().ContextFrames) != 0 {
		t.Fatal("acknowledged result replayed")
	}
}
func TestMutedCallBlocksDynamicWorkerActionsAndIgnoresStaleMuteRevision(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, target, newJournalFixture())
	muted, unmuted := true, false
	apply(t, f, Action{Action: "input-state", AttemptID: "call-a", Muted: &muted, InputRevision: 2})
	v := apply(t, f, Action{Action: "input-state", AttemptID: "call-a", Muted: &unmuted, InputRevision: 1})
	if !v.InputMuted || v.InputRevision != 2 {
		t.Fatal("stale input state unmuted the call")
	}
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "muted-request", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": "session_send", "arguments": map[string]any{"targetId": "worker", "text": "Ghost input must not send this"}}})
	if len(target.sent) != 0 || len(target.steered) != 0 {
		t.Fatal("muted input instructed the worker")
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success {
		t.Fatal("muted tool was not rejected explicitly")
	}
}
