package voice

import (
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestFollowUpWaitsForDistinctExecutionOwnership(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	connectFixture(t, f)
	submit := func(id string, seq uint64) {
		body, _ := json.Marshal(map[string]any{"type": "delegation.created", "item": map[string]any{"id": id, "type": "delegation", "target": "client", "content": []map[string]any{{"type": "input_text", "text": "Check my sessions"}}}})
		apply(t, f, Action{Action: "realtime-event", AttemptID: "call-a", EventSequence: seq, Event: body})
	}
	submit("first", 1)
	waitFor(t, "first executor receipt", func() bool { f.service.mu.Lock(); defer f.service.mu.Unlock(); return f.service.executionTurn != "" })
	submit("second", 2)
	f.service.mu.Lock()
	queued := len(f.service.executionQueue)
	first := f.service.delegations["first"]
	f.service.unlock()
	if queued != 1 || first.finished {
		t.Fatal("follow-up stole the active request")
	}
	pollToolFixture(t, f, Event{Method: "item/completed", Params: map[string]any{"turnId": "executor-turn", "item": map[string]any{"type": "agentMessage", "id": "first-answer", "phase": "final_answer", "text": "First result"}}})
	frames := f.View().ContextFrames
	if len(frames) != 1 || frames[0].DelegationID != "first" {
		t.Fatalf("wrong ownership: %+v", frames)
	}
	pollToolFixture(t, f, Event{Method: "turn/completed", Params: map[string]any{"turn": map[string]any{"id": "executor-turn"}}})
	waitFor(t, "follow-up executor receipt", func() bool {
		f.service.mu.Lock()
		defer f.service.mu.Unlock()
		return f.service.delegations["second"].turn != ""
	})
	pollToolFixture(t, f, Event{Method: "item/completed", Params: map[string]any{"turnId": "executor-turn", "item": map[string]any{"type": "agentMessage", "id": "second-answer", "phase": "final_answer", "text": "Second result"}}})
	frames = f.View().ContextFrames
	if len(frames) != 2 || frames[1].DelegationID != "second" {
		t.Fatalf("follow-up disappeared: %+v", frames)
	}
}

func TestNativeEventDuplicatesAndGapsDoNotDispatchActions(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	connectFixture(t, f)
	event := []byte(`{"type":"turn.done","turn":{"role":"user","transcript":"Hello"}}`)
	action := Action{Action: "realtime-event", AttemptID: "call-a", EventSequence: 1, Event: event}
	apply(t, f, action)
	apply(t, f, action)
	if len(f.View().Events) != 1 {
		t.Fatal("duplicate input was replayed")
	}
	action.EventSequence = 3
	if _, err := f.Apply(action); err == nil {
		t.Fatal("event gap accepted")
	}
	if len(f.View().Events) != 1 {
		t.Fatal("gap changed conversation")
	}
}

func TestTypedRequestWhileMutedHasOneOwnedResult(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	connectFixture(t, f)
	f.service.mu.Lock()
	f.service.state.State.InputMuted = true
	f.service.unlock()
	a := Action{Action: "text", AttemptID: "call-a", MessageID: "typed-1", Text: "Check my sessions"}
	apply(t, f, a)
	if _, err := f.Apply(a); err == nil {
		t.Fatal("duplicate typed request accepted")
	}
	waitFor(t, "typed executor receipt", func() bool {
		f.service.mu.Lock()
		defer f.service.mu.Unlock()
		return f.service.executionTurn != ""
	})
	pollToolFixture(t, f, Event{Method: "item/completed", Params: map[string]any{"turnId": "executor-turn", "item": map[string]any{"type": "agentMessage", "id": "typed-answer", "phase": "final_answer", "text": "One session"}}})
	frames := f.View().ContextFrames
	if len(frames) != 1 || frames[0].Kind != "typed-result" || frames[0].DelegationID != "" {
		t.Fatalf("typed result has wrong ownership: %+v", frames)
	}
}

func TestMutedTypedRequestCanUseBoundWorkerTools(t *testing.T) {
	worker := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, worker, newJournalFixture())
	muted := true
	apply(t, f, Action{Action: "input-state", AttemptID: "call-a", Muted: &muted, InputRevision: 1})
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "typed-tool", Text: "Tell the worker to run its tests"})
	waitFor(t, "typed receipt", func() bool {
		f.service.mu.Lock()
		defer f.service.mu.Unlock()
		return f.service.executionTurn != ""
	})
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "typed-send", "threadId": f.View().Session.ID, "turnId": "executor-turn", "namespace": "agenthail", "tool": "session_send", "arguments": map[string]any{"targetId": "worker", "text": "Run tests"}}})
	if len(worker.sent)+len(worker.steered) != 1 {
		t.Fatal("explicit typed action was blocked by microphone mute")
	}
}

func TestReplacementPeerDoesNotReuseTranscriptDisplayIdentity(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	connectFixture(t, f)
	f.service.mu.Lock()
	f.service.recordNativeTranscript("assistant", "Before transfer")
	f.service.nativeSequence = 0
	f.service.recordNativeTranscript("assistant", "After transfer")
	f.service.unlock()
	events := f.View().Events
	if len(events) != 2 || events[0].Sequence == events[1].Sequence {
		t.Fatal("replacement peer overwrote a transcript display identity")
	}
}
