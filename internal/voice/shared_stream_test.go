package voice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionReadKeepsNewestItemsWithinEncodedVoiceBudget(t *testing.T) {
	stream := newJournalFixture()
	for i := 0; i < 40; i++ {
		stream.initial = append(stream.initial, sessionstream.Event{ItemID: strconv.Itoa(i), Kind: "message", Role: "assistant", Body: strings.Repeat("\x01界", 4000)})
	}
	stream.initial[39].Body = "Actual latest answer"
	f := newFixture(t, fixtureOptions{target: &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude}}, stream: stream})
	f.service.mu.Lock()
	defer f.service.unlock()
	output, err := f.service.readSessionTool(context.Background(), "worker")
	if err != nil || len(output) > 64<<10 {
		t.Fatalf("bytes=%d error=%v", len(output), err)
	}
	var result struct {
		Items     []map[string]any `json:"items"`
		Truncated bool             `json:"truncated"`
		Omitted   int              `json:"omittedItems"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.Omitted == 0 || len(result.Items) == 0 || result.Items[len(result.Items)-1]["text"] != "Actual latest answer" {
		t.Fatalf("result=%+v", result)
	}
}

func TestSessionReadRetainsLatestAnswerWhileNewerActivityExceedsBudget(t *testing.T) {
	stream := newJournalFixture()
	stream.initial = append(stream.initial, sessionstream.Event{ItemID: "answer", Kind: "message", Role: "assistant", Body: "Actual latest answer"})
	for i := 0; i < 39; i++ {
		stream.initial = append(stream.initial, sessionstream.Event{ItemID: strconv.Itoa(i), Kind: "event", Body: strings.Repeat("\x01界", 4000)})
	}
	f := newFixture(t, fixtureOptions{target: &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex}}, stream: stream})
	f.service.mu.Lock()
	defer f.service.unlock()
	output, err := f.service.readSessionTool(context.Background(), "worker")
	if err != nil || len(output) > 64<<10 {
		t.Fatalf("bytes=%d error=%v", len(output), err)
	}
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) < 2 || result.Items[0]["text"] != "Actual latest answer" || result.Items[len(result.Items)-1]["itemId"] != "38" {
		t.Fatal("budget evicted the latest answer or the latest activity")
	}
}

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
	waitFor(t, "previous feed during transfer", func() bool { return len(workerItems(f.service)) == 1 })
	if target := f.View().Target; target == nil || target.ID != previous.session.ID {
		t.Fatal("candidate became selected before attachment")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("rejected transfer succeeded")
	}
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "after-failure", Version: 1, Kind: "message", Role: "assistant", Body: "Previous worker after rejection", Final: true}
	waitFor(t, "previous feed after rejected transfer", func() bool { return len(workerItems(f.service)) == 2 })
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
			f.provider.pending = append(f.provider.pending, Batch{Events: []Event{{Method: "item/tool/call", Params: map[string]any{"requestId": "blocked", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": variant.tool, "arguments": map[string]any{"targetId": "worker", "text": "Authorized instruction"}}}}})
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
			waitFor(t, "stale tool response", func() bool {
				f.provider.mu.Lock()
				defer f.provider.mu.Unlock()
				return len(f.provider.toolCalls) == 1
			})
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
	f.provider.pending = append(f.provider.pending, Batch{Events: []Event{{Method: "item/tool/call", Params: map[string]any{"requestId": "blocked", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": "session_list"}}}})
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
	waitFor(t, "cancelled catalog response", func() bool {
		f.provider.mu.Lock()
		defer f.provider.mu.Unlock()
		return len(f.provider.toolCalls) == 1 && !f.provider.toolCalls[0].success
	})
}

func TestVoiceStaleContextCannotChangeHangupMessage(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, target, newJournalFixture())
	generation := f.service.targetGeneration
	stopped := apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	f.service.mu.Lock()
	current := f.service.targetContextCurrent(context.Background(), generation, "call-a", &target.session)
	f.service.unlock()
	if current || f.View().Message != stopped.Message {
		t.Fatal("retired feed can affect hangup")
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
			f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": tool, "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": tool, "arguments": map[string]any{"targetId": "worker"}}})
			if !strings.Contains(f.View().Message, "Voice tool result delivery interrupted") {
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
	request := Event{Method: "item/tool/call", Params: map[string]any{"requestId": "transfer", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "worker"}}}
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

type unavailableJournalFixture struct{}

func (unavailableJournalFixture) PrepareSessionStream(context.Context, *surface.Session) (sessionstream.Subscription, error) {
	return sessionstream.Subscription{}, errors.New("journal attachment rejected")
}

func TestVoiceTransferDoesNotSucceedWithoutJournalBinding(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Name: "Worker", Transport: "uds"}}
	stream := newJournalFixture()
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	connectFixture(t, f)
	f.service.mu.Lock()
	f.service.stream = unavailableJournalFixture{}
	f.service.unlock()
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "transfer", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "worker"}}})
	if target := f.View().Target; target != nil {
		t.Fatalf("failed attachment retained target: %+v", target)
	}
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if len(f.provider.toolCalls) != 1 || f.provider.toolCalls[0].success {
		t.Fatal("transfer reported success without a worker journal subscription")
	}
}

func TestVoiceAssistantRemovalIsRetainedWithoutEmittingSpeech(t *testing.T) {
	stream := newJournalFixture()
	f := boundFixture(t, &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Transport: "uds"}}, stream)
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", Body: "Answer", Final: true}
	waitFor(t, "retained answer", func() bool { return len(workerItems(f.service)) == 1 })
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "answer", Version: 2, Operation: "remove"}
	waitFor(t, "removed answer", func() bool { return len(workerItems(f.service)) == 0 })
	if len(f.View().ContextFrames) != 0 {
		t.Fatal("removal emitted voice output")
	}
}

func TestVoiceBoundReadAndListToolsReturnJournalData(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindClaude, Name: "Claude", Transport: "uds"}}
	stream := newJournalFixture()
	stream.initial = []sessionstream.Event{{ItemID: "answer", Kind: "message", Role: "assistant", Body: "Current result", Final: true}}
	f := boundFixture(t, target, stream)
	for _, tool := range []string{"session_list", "session_read"} {
		f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": tool, "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": tool, "arguments": map[string]any{"targetId": "worker"}}})
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
				f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "return", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": "voice_return_to_orchestrator"}})
			}
			f.service.mu.Lock()
			f.service.target = func(context.Context, string) (*Target, error) {
				return &Target{Session: &other.session, Adapter: other}, nil
			}
			f.service.unlock()
			f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "other-instruction", "threadId": f.View().Session.ID, "namespace": "agenthail", "tool": "session_send", "arguments": map[string]any{"targetId": "other", "text": "Do the authorized work"}}})
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

func TestVoiceProgressThenFinalReplacesRetainedItem(t *testing.T) {
	stream := newJournalFixture()
	f := boundFixture(t, &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}, stream)
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "answer", Version: 1, Kind: "message", Role: "assistant", Body: "Partial"}
	stream.events <- sessionstream.Event{Seq: 14, ItemID: "answer", Version: 2, Kind: "message", Role: "assistant", Body: "Partial then complete", Final: true}
	waitFor(t, "latest retained item", func() bool {
		items := workerItems(f.service)
		return len(items) == 1 && items[0].Body == "Partial then complete"
	})
	if len(f.View().ContextFrames) != 0 {
		t.Fatal("journal produced unsolicited output")
	}
}

func TestVoiceWorkerContentNeverEntersOutputQueue(t *testing.T) {
	f := boundFixture(t, &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}, newJournalFixture())
	f.service.mu.Lock()
	for _, kind := range []string{"message", "tool_output", "reasoning", "event"} {
		f.service.retainWorkerItem(sessionstream.Event{ItemID: kind, Kind: kind, Body: "Untrusted observation"})
	}
	f.service.unlock()
	if len(f.View().ContextFrames) != 0 {
		t.Fatal("worker data became speech")
	}
}

func TestVoiceAutomaticWorkerUpdatesRemainRetainedDuringOperatorResponse(t *testing.T) {
	stream := newJournalFixture()
	f := boundFixture(t, &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}, stream)
	pollToolFixture(t, f, Event{Method: "item/completed", Params: map[string]any{"turnId": "unrelated", "item": map[string]any{"type": "agentMessage", "id": "unrelated", "text": "Not a voice answer"}}})
	stream.events <- sessionstream.Event{Seq: 13, ItemID: "status", Version: 1, Kind: "message", Role: "assistant", Body: "I am checking this call", Final: true}
	waitFor(t, "retained status", func() bool { return len(workerItems(f.service)) == 1 })
	if len(f.View().ContextFrames) != 0 {
		t.Fatal("independent streams produced voice output")
	}
}

func TestVoiceBindingHistoryUsesLatestLiveItemVersions(t *testing.T) {
	stream := newJournalFixture()
	stream.initial = []sessionstream.Event{{ItemID: "answer", Version: 1, Body: "Partial"}, {ItemID: "answer", Version: 2, Body: "Complete"}}
	f := boundFixture(t, &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}, stream)
	items := workerItems(f.service)
	if len(items) != 1 || items[0].Body != "Complete" {
		t.Fatalf("retained history=%v", items)
	}
}

func workerItems(service *Service) []sessionstream.Event {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.retainedWorkerContext()
}

func boundFixture(t *testing.T, target *targetFixture, stream *journalStreamFixture) *voiceFixture {
	f := newFixture(t, fixtureOptions{target: target, stream: stream})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)
	waitFor(t, "worker subscription", func() bool { f.service.mu.Lock(); defer f.service.mu.Unlock(); return f.service.targetCancel != nil })
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
	waitFor(t, "both independent worker messages", func() bool { return len(workerItems(f.service)) == 4 })
	if len(target.sent) != 0 {
		t.Fatal("observation sent worker input")
	}
	context, _ := json.Marshal(workerItems(f.service))
	if strings.Contains(string(context), "private reasoning") {
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

func TestVoiceTypedDiscussionNeverBlindlySendsToWorker(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	f := boundFixture(t, target, newJournalFixture())
	_, _ = f.Apply(Action{Action: "text", AttemptID: "call-a", MessageID: "text", Text: "What does that mean?"})
	if len(target.sent) != 0 {
		t.Fatal("typed discussion blindly sent to worker")
	}
}

func TestVoiceReturnCancelsFeedAndRejectsOldGeneration(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop"}}
	stream := newJournalFixture()
	f := boundFixture(t, target, stream)
	generation := f.service.targetGeneration
	apply(t, f, Action{Action: "transfer", TargetID: ""})
	select {
	case <-stream.cancelled:
	case <-time.After(time.Second):
		t.Fatal("old subscription leaked")
	}
	f.service.mu.Lock()
	current := f.service.targetContextCurrent(context.Background(), generation, "call-a", &target.session)
	f.service.unlock()
	if current || len(f.View().ContextFrames) != 0 || !f.View().RebindRequested {
		t.Fatal("return did not retire the previous conversation")
	}
}

func TestVoiceCloseDoesNotWaitForPhoneContextAcknowledgment(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "worker", Surface: surface.KindCodex, Name: "Worker", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target, stream: newJournalFixture()})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: "worker"})
	connectFixture(t, f)
	waitFor(t, "retained subscription", func() bool { f.service.mu.Lock(); defer f.service.mu.Unlock(); return f.service.targetCancel != nil })
	closed := make(chan struct{})
	go func() { f.service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel blocked context")
	}
}
