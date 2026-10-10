package voice

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type deadlineCheckingToolProvider struct {
	*fixtureProvider
}

func (p deadlineCheckingToolProvider) RespondDynamicToolCall(ctx context.Context, requestID, threadID string, success bool, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.fixtureProvider.RespondDynamicToolCall(ctx, requestID, threadID, success, text)
}

func pollToolFixture(t *testing.T, f *voiceFixture, events ...Event) {
	t.Helper()
	f.provider.mu.Lock()
	f.provider.pending = append(f.provider.pending, Batch{Events: events})
	f.provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	f.service.mu.Lock()
	err := f.service.poll(ctx)
	f.service.unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestToolTransferDoesNotBlockInputOrProgressSpeech(t *testing.T) {
	worker := &targetFixture{session: surface.Session{ID: "worker", Name: "Worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, worker, newJournalFixture())
	entered, release := make(chan struct{}), make(chan struct{})
	stream := &blockedVoiceStream{newJournalFixture(), func() { close(entered); <-release }}
	defer close(release)
	f.service.mu.Lock()
	f.service.stream = stream
	f.service.unlock()
	call := Event{Method: "item/tool/call", Params: map[string]any{"requestId": "transfer", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "turnId": "delegated", "arguments": map[string]any{"targetId": "worker"}}}
	pollToolFixture(t, f,
		Event{Method: "turn/started", Params: map[string]any{"turn": map[string]any{"id": "delegated"}}},
		Event{Method: "item/completed", Params: map[string]any{"turnId": "delegated", "item": map[string]any{"type": "userMessage", "content": []any{map[string]any{"text": "<realtime_delegation>\nTransfer please"}}}}}, call)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("transfer did not begin attaching")
	}
	// A cancelled event-poll context must not cancel the independently owned tool.
	time.Sleep(150 * time.Millisecond)
	pollToolFixture(t, f,
		Event{Method: "thread/realtime/transcript/delta", Params: map[string]any{"role": "user", "delta": "Are you still there?"}},
		Event{Sequence: 10, Method: "item/completed", Params: map[string]any{"turnId": "delegated", "item": map[string]any{"type": "agentMessage", "id": "progress", "phase": "commentary", "text": "I’m attaching the worker’s live updates."}}})
	spoken := f.provider.spoken()
	found := false
	for _, text := range spoken {
		found = found || strings.Contains(text, "attaching the worker")
	}
	if !found || !hasEventWith(f.View(), "thread/realtime/transcript/delta", "delta", "Are you still there?") {
		t.Fatalf("input/progress blocked while tool running: spoken=%v", spoken)
	}
	f.provider.mu.Lock()
	completed := len(f.provider.toolCalls)
	f.provider.mu.Unlock()
	if completed != 0 {
		t.Fatal("transfer completed before attachment")
	}
	// Release the original attachment without relying on another observer tick.
	release <- struct{}{}
	waitFor(t, "attached transfer result", func() bool {
		f.provider.mu.Lock()
		defer f.provider.mu.Unlock()
		return len(f.provider.toolCalls) == 1 && f.provider.toolCalls[0].success
	})
	if f.View().Target == nil || f.View().Target.ID != "worker" {
		t.Fatal("successful transfer did not commit its worker")
	}
}

func TestToolResponseRetryDoesNotRepeatTransfer(t *testing.T) {
	worker := &targetFixture{session: surface.Session{ID: "worker", Name: "Worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, worker, newJournalFixture())
	f.provider.mu.Lock()
	f.provider.toolErr = errors.New("temporary response transport failure")
	f.provider.mu.Unlock()
	call := Event{Method: "item/tool/call", Params: map[string]any{"requestId": "transfer", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "worker"}}}
	f.observe(call)
	generation := f.View().BindingGeneration
	f.provider.mu.Lock()
	f.provider.toolErr = nil
	f.provider.mu.Unlock()
	pollToolFixture(t, f, call, call)
	waitFor(t, "response retry", func() bool {
		f.provider.mu.Lock()
		defer f.provider.mu.Unlock()
		return len(f.provider.toolCalls) == 2
	})
	if f.View().BindingGeneration != generation {
		t.Fatal("response retry repeated transfer")
	}
	if strings.Contains(f.View().Message, "delivery interrupted") {
		t.Fatal("successful result retry left a stale delivery error")
	}
}

func TestExpiredToolDeadlineStillReturnsFailure(t *testing.T) {
	f := newFixture(t, fixtureOptions{stream: newJournalFixture()})
	connectFixture(t, f)
	f.service.mu.Lock()
	f.service.provider = deadlineCheckingToolProvider{f.provider}
	f.service.unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.service.mu.Lock()
	f.service.handleDynamicToolCall(ctx, Event{Method: "item/tool/call", Params: map[string]any{"requestId": "expired", "threadId": "operator", "namespace": "agenthail", "tool": "session_list"}})
	f.service.unlock()
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success || !strings.Contains(f.provider.toolCalls[0].text, "context canceled") {
		t.Fatalf("expired operation did not return its failure: %+v", f.provider.toolCalls)
	}
}

func TestToolCallsKeepProviderRequestOrder(t *testing.T) {
	f := newFixture(t, fixtureOptions{stream: newJournalFixture()})
	connectFixture(t, f)
	var events []Event
	for _, id := range []string{"first", "second", "third"} {
		events = append(events, Event{Method: "item/tool/call", Params: map[string]any{"requestId": id, "threadId": "operator", "namespace": "agenthail", "tool": "session_list"}})
	}
	f.observe(events...)
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 3 {
		t.Fatalf("results=%+v", f.provider.toolCalls)
	}
	for index, id := range []string{"first", "second", "third"} {
		if f.provider.toolCalls[index].requestID != id {
			t.Fatal("tool effects/results changed request order")
		}
	}
}
