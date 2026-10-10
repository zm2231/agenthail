package voice

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestOperatorBackendResponsesNeverCreateAnotherSpeechSubmission(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	apply(t, f, Action{Action: "prepare"})
	connectFixture(t, f)
	for _, turn := range []string{"first", "follow-up", "third"} {
		pollToolFixture(t, f,
			Event{Method: "turn/started", Params: map[string]any{"turn": map[string]any{"id": turn}}},
			Event{Method: "item/completed", Params: map[string]any{"turnId": turn, "item": map[string]any{"type": "userMessage", "content": []any{map[string]any{"text": "<realtime_delegation>\n<input>Follow up</input>\n</realtime_delegation>"}}}}},
			Event{Method: "item/completed", Params: map[string]any{"turnId": turn, "item": map[string]any{"type": "agentMessage", "id": turn, "phase": "commentary", "text": "Checking"}}},
			Event{Method: "item/completed", Params: map[string]any{"turnId": turn, "item": map[string]any{"type": "agentMessage", "id": turn + "-final", "phase": "final_answer", "text": "Done"}}})
	}
	requests, _, _ := f.provider.snapshot()
	for _, method := range requests {
		if method == "thread/realtime/appendSpeech" || method == "thread/realtime/appendText" {
			t.Fatalf("backend response resubmitted through %s", method)
		}
	}
	if len(f.service.conversation.pending) != 0 {
		t.Fatal("backend response entered worker context queue")
	}
}

func TestConversationLateFlatDoneCannotReleaseCanonicalSpeech(t *testing.T) {
	var c conversation
	now := time.Now()
	c.observe(Event{Method: "thread/realtime/transcript/delta", Params: map[string]any{"role": "assistant"}}, now)
	c.observe(Event{Method: "thread/realtime/item/started", Params: map[string]any{"item": map[string]any{"type": "transcriptSegment", "role": "assistant", "id": "current"}}}, now)
	c.observe(Event{Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "assistant"}}, now)
	if len(c.responses) != 1 || !c.responses["current"] {
		t.Fatal("late flat done released a canonical response")
	}
}

func TestVoiceContextQueueAndAcknowledgmentDoNotWriteCallState(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Transport: "uds"}}
	f := boundFixture(t, target, newJournalFixture())
	f.service.mu.Lock()
	f.service.path = filepath.Join(f.path, "cannot-create-under-a-file")
	generation := f.service.targetGeneration
	f.service.unlock()
	if !f.service.appendTargetContext(context.Background(), generation, "call-a", &target.session, map[string]any{"sequence": uint64(1), "item": map[string]any{"itemId": "update", "text": "Quiet latest update"}}) {
		t.Fatal("context queue depended on a filesystem write")
	}
	frames := f.View().ContextFrames
	apply(t, f, Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frames[len(frames)-1].ID})
}

func TestConversationResponseOwnershipAndCoalescing(t *testing.T) {
	var c conversation
	now := time.Now()
	event := func(method, id string) Event {
		return Event{Method: method, Params: map[string]any{"item": map[string]any{"type": "transcriptSegment", "id": id, "role": "assistant"}}}
	}
	c.observe(event("thread/realtime/item/started", "first"), now)
	c.observe(event("thread/realtime/item/started", "second"), now)
	c.stage(map[string]any{"sequence": uint64(1), "item": map[string]any{"itemId": "answer", "text": "partial"}})
	c.stage(map[string]any{"sequence": uint64(2), "item": map[string]any{"itemId": "answer", "text": "complete"}})
	c.observe(event("thread/realtime/item/completed", "first"), now)
	if c.ready(now.Add(time.Hour)) {
		t.Fatal("another response still owns the conversation")
	}
	c.observe(event("thread/realtime/item/completed", "second"), now)
	if c.ready(now) || !c.ready(now.Add(time.Second)) {
		t.Fatal("quiet interval not enforced")
	}
	if len(c.pending) != 1 || c.pending["answer"]["item"].(map[string]any)["text"] != "complete" {
		t.Fatal("snapshot was not coalesced")
	}
	c.reset()
	if c.ready(now.Add(time.Hour)) || len(c.responses) != 0 {
		t.Fatal("old ownership survived reset")
	}
}

func TestVoiceNativeContextAcknowledgmentIsAttemptBoundAndMonotonic(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Transport: "uds"}}
	f := boundFixture(t, target, newJournalFixture())
	frame := f.View().ContextFrames[0]
	if _, err := f.Apply(Action{Action: "context-ack", AttemptID: "old-call", ContextAck: frame.ID}); err == nil {
		t.Fatal("stale call acknowledged context")
	}
	if _, err := f.Apply(Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frame.ID + 1}); err == nil {
		t.Fatal("future acknowledgment accepted")
	}
	apply(t, f, Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frame.ID})
	apply(t, f, Action{Action: "context-ack", AttemptID: "call-a", ContextAck: frame.ID})
	if len(f.View().ContextFrames) != 0 {
		t.Fatal("acknowledged context replayed")
	}
	if len(f.service.View("other-phone").ContextFrames) != 0 {
		t.Fatal("foreign device received call context")
	}
	requests, _, _ := f.provider.snapshot()
	for _, method := range requests {
		if method == "thread/realtime/appendText" || method == "thread/realtime/appendSpeech" {
			t.Fatal("worker context used an implicit speech channel")
		}
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
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "muted-request", "threadId": "operator", "namespace": "agenthail", "tool": "session_send", "arguments": map[string]any{"targetId": "worker", "text": "Ghost input must not send this"}}})
	if len(target.sent) != 0 || len(target.steered) != 0 {
		t.Fatal("muted input instructed the worker")
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success {
		t.Fatal("muted tool was not rejected explicitly")
	}
}
