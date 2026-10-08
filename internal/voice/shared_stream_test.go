package voice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type journalStreamFixture struct {
	events      chan sessionstream.Event
	initial     []sessionstream.Event
	prepared    chan struct{}
	cancelled   chan struct{}
	once        sync.Once
	prepareOnce sync.Once
}

func newJournalFixture() *journalStreamFixture {
	return &journalStreamFixture{events: make(chan sessionstream.Event, 32), prepared: make(chan struct{}), cancelled: make(chan struct{})}
}

func (f *journalStreamFixture) PrepareSessionStream(context.Context, *surface.Session) (sessionstream.Subscription, error) {
	f.prepareOnce.Do(func() { close(f.prepared) })
	return sessionstream.Subscription{Cursor: 12, Initial: f.initial, Events: f.events, Cancel: func() { f.once.Do(func() { close(f.cancelled) }) }}, nil
}

func (f *journalStreamFixture) ListVoiceSessions(context.Context, string) ([]surface.Session, error) {
	return []surface.Session{{ID: "worker", Surface: surface.KindClaude, Name: "Claude"}}, nil
}

func (f *journalStreamFixture) ReadVoiceSession(context.Context, *surface.Session) ([]sessionstream.Event, error) {
	return f.initial, nil
}

type blockedVoiceCatalog struct {
	*journalStreamFixture
	entered chan struct{}
	release chan struct{}
}

type blockedVoiceAdapter struct {
	*targetFixture
	block func()
}

func (f *blockedVoiceAdapter) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	f.block()
	return f.targetFixture.Observe(ctx, session)
}

type blockedVoiceStream struct {
	*journalStreamFixture
	block func()
}

type rejectedVoiceStream struct {
	entered chan struct{}
	release chan struct{}
}

func (f *rejectedVoiceStream) PrepareSessionStream(ctx context.Context, _ *surface.Session) (sessionstream.Subscription, error) {
	close(f.entered)
	select {
	case <-f.release:
		return sessionstream.Subscription{}, errors.New("candidate feed unavailable")
	case <-ctx.Done():
		return sessionstream.Subscription{}, ctx.Err()
	}
}

func TestVoiceFailedTransferPreservesPreviousWorkerFeed(t *testing.T) {
	previous := &targetFixture{session: surface.Session{ID: "previous", Surface: surface.KindClaude, Transport: "uds"}}
	stream := newJournalFixture()
	f := boundFixture(t, previous, stream)
	candidate := &targetFixture{session: surface.Session{ID: "candidate", Surface: surface.KindCodex, Transport: "desktop"}}
	blocked := &rejectedVoiceStream{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(blocked.release)
	f.service.mu.Lock()
	f.service.stream = blocked
	f.service.target = func(context.Context, string) (*Target, error) {
		return &Target{Session: &candidate.session, Adapter: candidate}, nil
	}
	f.service.unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		f.service.mu.Lock()
		err := f.service.selectTarget(ctx, candidate.session.ID, true)
		f.service.unlock()
		done <- err
	}()
	select {
	case <-blocked.entered:
	case <-ctx.Done():
		t.Fatal("candidate subscription was not attempted")
	}
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "old-update", Version: 1, Kind: "message", Role: "assistant", Body: "Previous worker still running", Final: true}
	waitFor(t, "previous feed during transfer", func() bool { return len(contextMessages(f.provider)) == 2 })
	if target := f.View().Target; target == nil || target.ID != previous.session.ID {
		t.Fatal("candidate became selected before attachment")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("rejected transfer succeeded")
	}
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "after-failure", Version: 1, Kind: "message", Role: "assistant", Body: "Previous worker after rejection", Final: true}
	waitFor(t, "previous feed after rejected transfer", func() bool { return len(contextMessages(f.provider)) == 3 })
}

func (f *blockedVoiceStream) PrepareSessionStream(ctx context.Context, session *surface.Session) (sessionstream.Subscription, error) {
	f.block()
	return f.journalStreamFixture.PrepareSessionStream(ctx, session)
}

func TestVoiceBlockedOperationsKeepLifecycleResponsive(t *testing.T) {
	for _, variant := range []struct{ tool, stage string }{
		{"voice_transfer", "resolve"}, {"session_send", "resolve"}, {"session_interrupt", "resolve"},
		{"voice_transfer", "prepare"}, {"session_send", "observe"}, {"session_interrupt", "observe"},
	} {
		t.Run(variant.tool+"/"+variant.stage, func(t *testing.T) {
			target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}, observation: &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn"}, capabilities: surface.Capabilities{Interrupt: true, Steer: true}}
			f := boundFixture(t, target, newJournalFixture())
			attempt := f.View().AttemptID
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			block := func() { close(entered); <-release }
			f.service.mu.Lock()
			f.service.target = func(context.Context, string) (*Target, error) {
				if variant.stage == "resolve" {
					block()
				}
				if variant.stage == "observe" {
					return &Target{Session: &target.session, Adapter: &blockedVoiceAdapter{target, block}}, nil
				}
				return &Target{Session: &target.session, Adapter: target}, nil
			}
			if variant.stage == "prepare" {
				f.service.stream = &blockedVoiceStream{newJournalFixture(), block}
			}
			f.service.unlock()
			f.provider.mu.Lock()
			f.provider.pending = append(f.provider.pending, Batch{Events: []Event{{Method: "item/tool/call", Params: map[string]any{"requestId": "blocked", "threadId": "operator", "namespace": "agenthail", "tool": variant.tool, "arguments": map[string]any{"targetId": "worker", "text": "Authorized instruction"}}}}})
			f.provider.mu.Unlock()
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			go func() { f.service.mu.Lock(); err := f.service.poll(ctx); f.service.unlock(); done <- err }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("operation did not block")
			}
			responsive := make(chan error, 1)
			go func() {
				_, err := f.Apply(Action{Action: "heartbeat", AttemptID: attempt})
				if err == nil {
					_, err = f.Apply(Action{Action: "stop", AttemptID: attempt})
				}
				responsive <- err
			}()
			select {
			case err := <-responsive:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatal("operation retained the lifecycle lock")
			}
			unblock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("stale operation did not finish")
			}
			if len(target.sent) != 0 || len(target.steered) != 0 || target.interrupts != 0 || f.View().Phase == "connected" {
				t.Fatal("stale operation changed or resurrected the call")
			}
			f.provider.mu.Lock()
			defer f.provider.mu.Unlock()
			if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success {
				t.Fatal("stale tool result reported success")
			}
		})
	}
}

func (f *blockedVoiceCatalog) ListVoiceSessions(context.Context, string) ([]surface.Session, error) {
	close(f.entered)
	<-f.release
	return nil, nil
}

func TestVoiceBlockedCatalogDoesNotBlockHeartbeatOrHangup(t *testing.T) {
	stream := &blockedVoiceCatalog{journalStreamFixture: newJournalFixture(), entered: make(chan struct{}), release: make(chan struct{})}
	f := newFixture(t, fixtureOptions{stream: stream})
	connectFixture(t, f)
	attempt := f.View().AttemptID
	f.provider.mu.Lock()
	f.provider.pending = append(f.provider.pending, Batch{Events: []Event{{Method: "item/tool/call", Params: map[string]any{"requestId": "blocked", "threadId": "operator", "namespace": "agenthail", "tool": "session_list"}}}})
	f.provider.mu.Unlock()
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go func() {
		f.service.mu.Lock()
		err := f.service.poll(ctx)
		f.service.unlock()
		done <- err
	}()
	select {
	case <-stream.entered:
	case <-time.After(time.Second):
		t.Fatal("tool did not enter catalog read")
	}
	defer close(stream.release)
	responsive := make(chan error, 1)
	go func() {
		_, err := f.Apply(Action{Action: "heartbeat", AttemptID: attempt})
		if err == nil {
			_, err = f.Apply(Action{Action: "stop", AttemptID: attempt})
		}
		responsive <- err
	}()
	select {
	case err := <-responsive:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blocked catalog retained the call lifecycle lock")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not return at its deadline")
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success {
		t.Fatal("blocked read did not return a failed tool result")
	}
}

func TestVoiceStaleContextCannotChangeHangupMessage(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, target, newJournalFixture())
	f.service.mu.Lock()
	generation, attempt := f.service.targetGeneration, f.service.state.State.AttemptID
	f.service.speechTimeout = 200 * time.Millisecond
	f.service.unlock()
	entered := make(chan struct{})
	f.provider.mu.Lock()
	f.provider.speaking, f.provider.speakingText = entered, "stale-context-sentinel"
	f.provider.mu.Unlock()
	done := make(chan bool, 1)
	go func() {
		done <- f.service.appendTargetContext(context.Background(), generation, attempt, &target.session, map[string]any{"text": "stale-context-sentinel"})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("context request did not block")
	}
	stopped := apply(t, f, Action{Action: "stop", AttemptID: attempt})
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("stale context was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("context did not finish")
	}
	if f.View().Message != stopped.Message {
		t.Fatal("stale context changed hangup message")
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved diskState
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.State.Message != stopped.Message {
		t.Fatal("stale context persisted a new message")
	}
}

func TestVoiceStaleTransferCannotRestoreRouteAfterReturn(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, target, newJournalFixture())
	attempt := f.View().AttemptID
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	stream := newJournalFixture()
	f.service.mu.Lock()
	f.service.stream = &blockedVoiceStream{stream, func() { close(entered); <-release }}
	f.service.unlock()
	done := make(chan error, 1)
	go func() {
		_, err := f.Apply(Action{Action: "transfer", AttemptID: attempt, TargetID: "worker"})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("transfer did not block")
	}
	returned := apply(t, f, Action{Action: "transfer", AttemptID: attempt})
	if returned.Target != nil {
		t.Fatal("return did not clear target")
	}
	unblock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale transfer succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("transfer did not finish")
	}
	if f.View().Target != nil || f.View().Message != returned.Message {
		t.Fatal("stale transfer restored a previous route")
	}
	select {
	case <-stream.cancelled:
	default:
		t.Fatal("stale subscription was not cancelled")
	}
}

func TestVoiceCatalogRenewalRetainsHistoryAndSelectedTarget(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Name: "Claude", Transport: "uds"}}
	stream := newJournalFixture()
	stream.initial = []sessionstream.Event{{ItemID: "conversation", Kind: "message", Role: "user", Body: "Keep the scope narrow"}}
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "worker"})
	f.service.mu.Lock()
	f.service.state.State.SkillDigest = "older-tool-catalog"
	f.service.unlock()
	apply(t, f, Action{Action: "prepare"})
	if got := f.View(); got.Session.ID != "operator-2" || got.Target == nil || got.Target.ID != "worker" || got.CodingProvider != "claude" {
		t.Fatalf("renewal state=%+v", got)
	}
	if !strings.Contains(f.provider.instructions, "Keep the scope narrow") {
		t.Fatal("prior conversation lost")
	}
	apply(t, f, Action{Action: "prepare"})
	if _, _, creates := f.provider.snapshot(); creates != 2 {
		t.Fatalf("catalog renewal repeated: %d", creates)
	}
}

func TestVoiceToolResponseFailureIsVisibleAndPersisted(t *testing.T) {
	for _, tool := range []string{"session_read", "session_list", "unsupported"} {
		t.Run(tool, func(t *testing.T) {
			f := newFixture(t, fixtureOptions{target: &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex}}, stream: newJournalFixture()})
			apply(t, f, Action{Action: "prepare"})
			connectFixture(t, f)
			f.provider.mu.Lock()
			f.provider.toolErr = errors.New("response transport unavailable")
			f.provider.mu.Unlock()
			f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": tool, "threadId": "operator", "namespace": "agenthail", "tool": tool, "arguments": map[string]any{"targetId": "worker"}}})
			if !strings.Contains(f.View().Message, "Voice tool response could not be returned") {
				t.Fatal("tool response failure was hidden")
			}
			data, err := os.ReadFile(f.path)
			var stored struct {
				State State `json:"state"`
			}
			if err == nil {
				err = json.Unmarshal(data, &stored)
			}
			if err != nil || stored.State.Message != f.View().Message {
				t.Fatalf("tool failure not persisted: %v", err)
			}
		})
	}
}

func TestVoiceRepeatedToolRequestDoesNotRepeatCommittedTransfer(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target, stream: newJournalFixture()})
	connectFixture(t, f)
	request := Event{Method: "item/tool/call", Params: map[string]any{"requestId": "transfer", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "worker"}}}
	f.provider.toolErr = errors.New("response transport unavailable")
	f.observe(request)
	if got := f.View().Target; got == nil || got.ID != "worker" {
		t.Fatal("transfer did not commit before response failure")
	}
	f.service.mu.Lock()
	generation := f.service.targetGeneration
	f.service.mu.Unlock()
	f.provider.mu.Lock()
	f.provider.toolErr = nil
	f.provider.mu.Unlock()
	f.observe(request)
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	if f.service.targetGeneration != generation {
		t.Fatal("duplicate tool request changed the target binding")
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 2 || f.provider.toolCalls[0].text != f.provider.toolCalls[1].text {
		t.Fatal("duplicate tool request did not reuse its committed result")
	}
}

func TestVoiceTransferDoesNotSucceedWithoutRealtimeBinding(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Name: "Worker", Transport: "uds"}}
	stream := newJournalFixture()
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	connectFixture(t, f)
	f.provider.failRequests(errors.New("realtime attachment rejected"))
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "transfer", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "worker"}}})
	if target := f.View().Target; target != nil {
		t.Fatalf("failed attachment retained target: %+v", target)
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success {
		t.Fatal("transfer reported success before realtime accepted target context")
	}
	select {
	case <-stream.cancelled:
	default:
		t.Fatal("failed attachment leaked its subscription")
	}
}

func TestVoiceAssistantRemovalIsForwardedWithoutReplayingText(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	stream := newJournalFixture()
	f := boundFixture(t, target, stream)
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", Body: "Obsolete response", Final: true}
	waitFor(t, "assistant response", func() bool { return len(contextMessages(f.provider)) == 2 })
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "answer", Version: 2, Operation: "remove", Kind: "message", Role: "assistant", Body: "Obsolete response", Final: true}
	waitFor(t, "assistant removal", func() bool { return len(contextMessages(f.provider)) == 3 })
	item := contextMessages(f.provider)[2]["item"].(map[string]any)
	if item["operation"] != "remove" || item["text"] != nil {
		t.Fatalf("removal replayed text: %v", item)
	}
}

func TestVoiceBoundReadAndListToolsReturnJournalData(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Name: "Claude", Transport: "uds"}}
	stream := newJournalFixture()
	stream.initial = []sessionstream.Event{{ItemID: "answer", Kind: "message", Role: "assistant", Body: "Current result", Final: true}}
	f := boundFixture(t, target, stream)
	for _, tool := range []string{"session_list", "session_read"} {
		f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": tool, "threadId": "operator", "namespace": "agenthail", "tool": tool, "arguments": map[string]any{"targetId": "worker"}}})
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 2 || !f.provider.toolCalls[0].success || !f.provider.toolCalls[1].success {
		t.Fatalf("tools=%+v", f.provider.toolCalls)
	}
	if !strings.Contains(f.provider.toolCalls[1].text, `"source":"journal"`) || !strings.Contains(f.provider.toolCalls[1].text, "Current result") {
		t.Fatal("read did not return journal")
	}
}

func TestVoiceSendToolRespectsSelectedWorkerBoundary(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(map[bool]string{true: "selected", false: "orchestrator"}[bound], func(t *testing.T) {
			selected := &targetFixture{session: surface.Session{ID: "selected", Surface: surface.KindCodex, Transport: "desktop"}}
			other := &targetFixture{session: surface.Session{ID: "other", Surface: surface.KindCodex, Transport: "desktop"}}
			f := boundFixture(t, selected, newJournalFixture())
			if !bound {
				f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "return", "threadId": "operator", "namespace": "agenthail", "tool": "voice_return_to_orchestrator"}})
			}
			f.service.mu.Lock()
			f.service.target = func(context.Context, string) (*Target, error) {
				return &Target{Session: &other.session, Adapter: other}, nil
			}
			f.service.unlock()
			f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "other-instruction", "threadId": "operator", "namespace": "agenthail", "tool": "session_send", "arguments": map[string]any{"targetId": "other", "text": "Do the authorized work"}}})
			f.provider.mu.Lock()
			response := f.provider.toolCalls[len(f.provider.toolCalls)-1]
			f.provider.mu.Unlock()
			if bound && (response.success || len(other.sent) != 0) {
				t.Fatal("selected call wrote to a different worker")
			}
			if !bound && (!response.success || len(other.sent) != 1) {
				t.Fatal("orchestrator could not send an intentional instruction")
			}
		})
	}
}

func TestVoiceProgressThenFinalDoesNotRepeatSpokenPrefix(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}}
	stream := newJournalFixture()
	f := boundFixture(t, target, stream)
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", Body: "First sentence. Second sentence. More"}
	waitFor(t, "progress context", func() bool { return len(contextMessages(f.provider)) == 2 })
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "answer", Version: 2, Kind: "message", Role: "assistant", Body: "First sentence. Second sentence. More done", Final: true}
	waitFor(t, "final context", func() bool { return len(contextMessages(f.provider)) == 3 })
	item := contextMessages(f.provider)[2]["item"].(map[string]any)
	if item["text"] != " done" || item["textMode"] != "delta" {
		t.Fatalf("final replay=%v", item)
	}
}

func TestVoiceBindingHistoryUsesLatestLiveItemVersions(t *testing.T) {
	stream := newJournalFixture()
	stream.initial = []sessionstream.Event{
		{Seq: 1, ItemID: "answer", Kind: "message", Role: "assistant", Body: "Partial"},
		{Seq: 2, ItemID: "removed", Kind: "message", Role: "assistant", Body: "Obsolete"},
		{Seq: 3, ItemID: "answer", Kind: "message", Role: "assistant", Body: "Complete", Final: true},
		{Seq: 4, ItemID: "removed", Operation: "remove", Kind: "message", Role: "assistant"},
		{Seq: 5, ItemID: "hidden", Kind: "reasoning", Role: "assistant", Body: "Private"},
	}
	f := boundFixture(t, &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}, stream)
	recent := contextMessages(f.provider)[0]["recent"].([]any)
	if len(recent) != 1 || recent[0].(map[string]any)["text"] != "Complete" {
		t.Fatalf("binding history=%v", recent)
	}
}

func contextMessages(provider *fixtureProvider) []map[string]any {
	var result []map[string]any
	for _, text := range provider.spoken() {
		_, body, ok := strings.Cut(text, "\n")
		if !ok {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(body), &value) == nil {
			result = append(result, value)
		}
	}
	return result
}

func boundFixture(t *testing.T, target *targetFixture, stream *journalStreamFixture) *voiceFixture {
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)
	waitFor(t, "initial worker context", func() bool { return len(contextMessages(f.provider)) == 1 })
	return f
}

func TestVoiceContinuouslyObservesClaudePeerWithoutSending(t *testing.T) {
	stream := newJournalFixture()
	stream.initial = []sessionstream.Event{{ItemID: "history", Version: 1, Kind: "message", Role: "assistant", Body: "Previous work", Final: true}}
	target := &targetFixture{session: surface.Session{ID: "peer", Surface: surface.KindClaude, Name: "Claude", Transport: "uds"}}
	f := boundFixture(t, target, stream)
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", TurnID: "already-running", Body: "First update", Final: true}
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", TurnID: "already-running", Body: "First update", Final: true}
	stream.events <- sessionstream.Event{Seq: 15, ItemID: "reasoning", Version: 1, Kind: "reasoning", Role: "assistant", Body: "private reasoning"}
	stream.events <- sessionstream.Event{Seq: 16, ItemID: "second", Version: 1, Kind: "message", Role: "assistant", TurnID: "next-turn", Body: "Second update", Final: true}
	waitFor(t, "both independent worker messages", func() bool { return len(contextMessages(f.provider)) == 3 })
	if len(target.sent) != 0 {
		t.Fatal("observation sent worker input")
	}
	context := f.provider.spoken()
	if strings.Contains(strings.Join(context, "\n"), "private reasoning") {
		t.Fatal("hidden reasoning leaked")
	}
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "request-1", Text: "Make the change"})
	if len(target.sent) != 1 || target.sent[0] != "Make the change" {
		t.Fatalf("explicit instruction=%v", target.sent)
	}
	if _, err := f.Apply(Action{Action: "delegate", AttemptID: "call-a", MessageID: "request-1", Text: "Make the change"}); err == nil || len(target.sent) != 1 {
		t.Fatal("instruction replayed")
	}
}

func TestVoiceBusyInstructionSteersAndNeverQueues(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{true: "steer", false: "unsupported"}[capable], func(t *testing.T) {
			target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}, observation: &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "active"}, capabilities: surface.Capabilities{Send: true, Stream: true, Steer: capable}}
			f := boundFixture(t, target, newJournalFixture())
			_, err := f.Apply(Action{Action: "delegate", AttemptID: "call-a", MessageID: "correction", Text: "Use the other approach"})
			if capable && (err != nil || len(target.steered) != 1) {
				t.Fatalf("steer: %v, %v", target.steered, err)
			}
			if !capable && err == nil {
				t.Fatal("unsupported steer succeeded")
			}
			if len(target.sent) != 0 {
				t.Fatal("busy instruction sent or queued")
			}
		})
	}
}

func TestVoiceTypedDiscussionStaysWithIntermediary(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}}
	f := boundFixture(t, target, newJournalFixture())
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "discussion", Text: "What does that mean?"})
	if len(target.sent) != 0 {
		t.Fatal("typed discussion blindly forwarded")
	}
	if texts := f.provider.spoken(); texts[len(texts)-1] != "What does that mean?" {
		t.Fatalf("voice input=%v", texts)
	}
}

func TestVoiceReturnCancelsFeedAndRejectsOldGeneration(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}}
	stream := newJournalFixture()
	f := boundFixture(t, target, stream)
	generation := f.service.targetGeneration
	apply(t, f, Action{Action: "transfer", TargetID: ""})
	select {
	case <-stream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("old subscription leaked")
	}
	if f.service.appendTargetContext(context.Background(), generation, "call-a", &target.session, map[string]any{"type": "stale"}) {
		t.Fatal("old target context accepted")
	}
	if len(contextMessages(f.provider)) != 1 {
		t.Fatal("old target spoken after return")
	}
}

func TestVoiceCloseCancelsBlockedContextRequest(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target, stream: newJournalFixture()})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "worker"})
	speaking := make(chan struct{})
	f.provider.speaking, f.provider.speakingText = speaking, "Selected-session context"
	connectFixture(t, f)
	select {
	case <-speaking:
	case <-time.After(time.Second):
		t.Fatal("context request never started")
	}
	closed := make(chan struct{})
	go func() { f.service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel blocked context")
	}
}
