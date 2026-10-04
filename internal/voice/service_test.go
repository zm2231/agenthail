package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
)

type fixtureProvider struct {
	mu           sync.Mutex
	creates      int
	instructions string
	requests     []string
	params       []map[string]any
	err          error
	createErr    error
	batch        Batch
	interrupts   int
	toolCalls    []struct {
		requestID string
		success   bool
		text      string
	}
}

type targetFixture struct {
	session         surface.Session
	sent            []string
	stream          func(func(surface.StreamEvent))
	turnID          string
	observation     *surface.TurnObservation
	interrupts      int
	interruptTurnID string
	activeTurnID    string
	capabilities    surface.Capabilities
}

func (f *targetFixture) Name() surface.SurfaceKind { return f.session.Surface }
func (f *targetFixture) List(context.Context) ([]surface.Session, error) {
	return []surface.Session{f.session}, nil
}
func (f *targetFixture) Resolve(_ context.Context, id string) (*surface.Session, error) {
	if id != f.session.ID {
		return nil, errors.New("unknown target")
	}
	copy := f.session
	return &copy, nil
}
func (f *targetFixture) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	if f.observation != nil {
		return f.observation, nil
	}
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
}
func (f *targetFixture) Send(_ context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	f.sent = append(f.sent, message)
	turnID := f.turnID
	if turnID == "" {
		turnID = "target-turn"
	}
	return &surface.SendResult{UUID: turnID, Accepted: true}, nil
}
func (f *targetFixture) Reply(context.Context, *surface.Session, int) (*surface.ReplyResult, error) {
	return &surface.ReplyResult{}, nil
}
func (f *targetFixture) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return nil, nil
}
func (f *targetFixture) Stream(_ context.Context, _ *surface.Session, turnID string, callback func(surface.StreamEvent), _ time.Duration) error {
	if turnID != "target-turn" {
		return errors.New("wrong turn")
	}
	if f.stream != nil {
		f.stream(callback)
	}
	return nil
}
func (f *targetFixture) GoalSet(context.Context, *surface.Session, string) error {
	return surface.ErrUnsupported
}
func (f *targetFixture) GoalClear(context.Context, *surface.Session) error {
	return surface.ErrUnsupported
}
func (f *targetFixture) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, surface.ErrUnsupported
}
func (f *targetFixture) Compact(context.Context, *surface.Session) error {
	return surface.ErrUnsupported
}
func (f *targetFixture) Model(context.Context, *surface.Session, string) (string, error) {
	return "", surface.ErrUnsupported
}
func (f *targetFixture) Interrupt(context.Context, *surface.Session) error {
	f.interrupts++
	return nil
}
func (f *targetFixture) InterruptTurn(_ context.Context, _ *surface.Session, turnID string) error {
	if f.activeTurnID != "" && f.activeTurnID != turnID {
		return errors.New("selected turn changed before interruption")
	}
	f.interrupts++
	f.interruptTurnID = turnID
	return nil
}
func (f *targetFixture) Steer(context.Context, *surface.Session, string) error {
	return surface.ErrUnsupported
}
func (f *targetFixture) Capabilities() surface.Capabilities {
	if f.capabilities.Interrupt {
		return f.capabilities
	}
	return surface.Capabilities{Send: true, Stream: true}
}

func (p *fixtureProvider) Create(_ context.Context, cwd, instructions string) (*surface.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creates++
	p.instructions = instructions
	id := "operator"
	if p.creates > 1 {
		id = fmt.Sprintf("operator-%d", p.creates)
	}
	return &surface.Session{ID: id, Surface: surface.KindCodex, Name: "Voice operator", Cwd: cwd, Transport: "desktop"}, p.createErr
}
func (p *fixtureProvider) Cursor(context.Context) (Cursor, error) {
	return Cursor{Source: "fixture", Position: 10}, nil
}
func (p *fixtureProvider) Poll(context.Context, Cursor, string) (Batch, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.batch
	p.batch.Events = nil
	return b, nil
}

func (p *fixtureProvider) RespondDynamicToolCall(_ context.Context, requestID string, success bool, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolCalls = append(p.toolCalls, struct {
		requestID string
		success   bool
		text      string
	}{requestID, success, text})
	return nil
}
func (p *fixtureProvider) Request(_ context.Context, _ *surface.Session, method string, params map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, method)
	p.params = append(p.params, params)
	return p.err
}
func (p *fixtureProvider) Interrupt(context.Context, *surface.Session) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.interrupts++
	return nil
}

func fixture(t *testing.T) (*Service, *fixtureProvider) {
	t.Helper()
	p := &fixtureProvider{}
	s := New(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail")
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	return s, p
}

func apply(t *testing.T, s *Service, a Action) State {
	t.Helper()
	v, err := s.Apply(context.Background(), "phone", a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func observeFixture(t *testing.T, s *Service, p *fixtureProvider, events ...Event) {
	t.Helper()
	p.mu.Lock()
	p.batch = Batch{Cursor: Cursor{Source: "fixture", Position: 20}, Events: events}
	p.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func startFixture(t *testing.T, s *Service) {
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"})
}

func TestPersistentOperatorLoadsLiteralSkillWithoutStartingTurn(t *testing.T) {
	s, p := fixture(t)
	first := apply(t, s, Action{Action: "prepare"})
	second := apply(t, s, Action{Action: "prepare"})
	if first.Session.ID != second.Session.ID || p.creates != 1 || len(p.requests) != 0 {
		t.Fatalf("duplicate operator or startup turn: %+v", p)
	}
	if p.instructions != OperatorInstructions("/fixture/agenthail") || first.SkillDigest == "" {
		t.Fatal("operator did not receive the packaged skill contract")
	}
	reopened := New(s.path, p, nil, "/fixture/agenthail")
	if v, err := reopened.Apply(context.Background(), "phone", Action{Action: "prepare"}); err != nil || v.Session.ID != "operator" || p.creates != 1 {
		t.Fatalf("resume v=%+v err=%v creates=%d", v, err, p.creates)
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private state: %v %v", info, err)
	}
}

func TestNewOperatorCreatesDistinctPersistentThreadAndRetainsOldThread(t *testing.T) {
	p := &fixtureProvider{}
	var registered []string
	path := filepath.Join(t.TempDir(), "voice", "operator.json")
	s := New(path, p, func(session surface.Session) error {
		registered = append(registered, session.ID)
		return nil
	}, "/fixture/agenthail")
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	first := apply(t, s, Action{Action: "prepare"})
	first.Events = []Event{{Sequence: 1, Method: "thread/realtime/transcript/done"}}
	s.state.State = first
	second := apply(t, s, Action{Action: "new"})
	if first.Session.ID == second.Session.ID || second.Session.ID != "operator-2" || p.creates != 2 {
		t.Fatalf("new operator was not distinct: first=%+v second=%+v creates=%d", first.Session, second.Session, p.creates)
	}
	if len(second.Events) != 0 || second.Phase != "ready" {
		t.Fatalf("new operator inherited prior conversation state: %+v", second)
	}
	if !reflect.DeepEqual(registered, []string{"operator", "operator-2"}) {
		t.Fatalf("old and new operators were not both registered: %v", registered)
	}
	reopened := New(s.path, p, nil, "/fixture/agenthail")
	if got := reopened.View("phone"); got.Session == nil || got.Session.ID != "operator-2" || len(got.Events) != 0 {
		t.Fatalf("new operator identity was not durable: %+v", got)
	}
}

func TestNewOperatorFailsClosedAcrossActiveAndUnknownCreation(t *testing.T) {
	s, p := fixture(t)
	first := apply(t, s, Action{Action: "prepare"})
	startFixture(t, s)
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "new"}); err == nil || p.creates != 1 {
		t.Fatal("active call allowed operator replacement")
	}
	s.state.State.Phase = "ended"
	p.createErr = surface.DeliveryOutcomeUnknown(errors.New("lost replacement reply"))
	if got, err := s.Apply(context.Background(), "phone", Action{Action: "new"}); err == nil || got.Session.ID != first.Session.ID || got.Phase != "creating" {
		t.Fatalf("unknown replacement discarded the inspectable prior thread: got=%+v err=%v", got, err)
	}
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "new"}); err == nil || p.creates != 2 {
		t.Fatal("unknown replacement was retried")
	}
}

func TestCallLifecycleIsAudioSeparateFromWork(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	if v := s.View("phone"); v.Phase != "starting" || v.SDP != "" {
		t.Fatalf("accepted RPC is not connected: %+v", v)
	}
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}})
	if s.View("phone").Phase != "starting" {
		t.Fatal("started event is not an SDP answer")
	}
	observeFixture(t, s, p, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	if s.View("phone").Phase != "negotiating" {
		t.Fatal("answer was not exposed")
	}
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, s, Action{Action: "stop", AttemptID: "call-a"})
	if s.View("phone").Phase != "stopping" || p.interrupts != 0 {
		t.Fatal("hangup must await closure and not interrupt agent work")
	}
	observeFixture(t, s, p, Event{Method: "thread/realtime/closed", Params: map[string]any{}})
	apply(t, s, Action{Action: "start", AttemptID: "call-b", SDP: "v=0 offer-b"})
	if p.creates != 1 || !reflect.DeepEqual(p.requests, []string{"thread/realtime/start", "thread/realtime/stop", "thread/realtime/start"}) {
		t.Fatalf("flow: %+v", p.requests)
	}
	apply(t, s, Action{Action: "interrupt"})
	if p.interrupts != 1 {
		t.Fatal("explicit interrupt did not use the native operation")
	}
}

func TestUnknownStartupIsNotReplayed(t *testing.T) {
	s, p := fixture(t)
	apply(t, s, Action{Action: "prepare"})
	p.err = errors.New("response lost")
	a := Action{Action: "start", AttemptID: "call-a", SDP: "v=0 offer"}
	if v, err := s.Apply(context.Background(), "phone", a); err == nil || v.Phase != "unknown" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	apply(t, s, a)
	if len(p.requests) != 1 {
		t.Fatal("unknown startup was replayed")
	}
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "start", AttemptID: "call-b", SDP: "v=0 new"}); err == nil {
		t.Fatal("replaced an unconfirmed call")
	}
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 delayed answer"}})
	if s.View("phone").Phase != "negotiating" {
		t.Fatal("late acceptance did not reconcile")
	}
	reopened := New(s.path, p, nil, "/fixture/agenthail")
	if reopened.View("phone").Phase != "unknown" || reopened.View("phone").SDP != "" {
		t.Fatal("restart pretended to retain a physical audio connection")
	}
}

func TestDeviceLeaseRejectsTakeoverAndHidesSDP(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 private"}})
	if v := s.View("other-phone"); !v.Occupied || v.SDP != "" {
		t.Fatalf("leaked call: %+v", v)
	}
	for _, action := range []string{"prepare", "start", "connected", "text", "stop", "interrupt"} {
		if _, err := s.Apply(context.Background(), "other-phone", Action{Action: action, AttemptID: "call-a"}); err == nil {
			t.Fatalf("%s takeover accepted", action)
		}
	}
}

func TestOperatorSourceLifecycleTracksOnlyActiveTransitions(t *testing.T) {
	p := &fixtureProvider{}
	var calls []string
	s := NewWithTargetsAndOperatorSource(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", nil, delivery.Dispatcher{}, func(session *surface.Session, active bool) {
		calls = append(calls, session.ID+":"+strconv.FormatBool(active))
	})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, s, Action{Action: "stop", AttemptID: "call-a"})
	observeFixture(t, s, p, Event{Method: "thread/realtime/closed", Params: map[string]any{}})
	if !reflect.DeepEqual(calls, []string{"operator:true", "operator:false"}) {
		t.Fatalf("operator source calls=%v", calls)
	}
}

func TestTextUnknownReceiptPreventsDuplicate(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	p.err = errors.New("response lost")
	a := Action{Action: "text", AttemptID: "call-a", MessageID: "message-a", Text: "Please inspect my agents"}
	v, err := s.Apply(context.Background(), "phone", a)
	if err == nil || v.TextReceipt != "unknown:message-a" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if _, err = s.Apply(context.Background(), "phone", a); err == nil || len(p.requests) != 2 {
		t.Fatal("unknown text was replayed")
	}
}

func TestDelegationUsesSelectedTargetAndOnlySpeaksCorrelatedTurn(t *testing.T) {
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Disposable Claude", Transport: "browser"}}
	spoken := make(chan struct{})
	target.stream = func(callback func(surface.StreamEvent)) {
		callback(surface.StreamEvent{Kind: "text", Text: "The correlated answer."})
		close(spoken)
	}
	s := NewWithTargets(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return &Target{Session: resolved, Adapter: target}, nil
	}, delivery.Dispatcher{})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "select", TargetID: "target-a"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, s, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect the current failure"})
	select {
	case <-spoken:
	case <-time.After(time.Second):
		t.Fatal("correlated target stream did not reach the audio handoff")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !reflect.DeepEqual(target.sent, []string{"Inspect the current failure"}) {
		t.Fatalf("wrong target dispatch: %v", target.sent)
	}
	var spoke bool
	for _, params := range p.params {
		if params["text"] == "The correlated answer." {
			spoke = true
		}
	}
	if !spoke {
		t.Fatalf("no correlated result was handed to audio: %+v", p.params)
	}
	v := s.View("phone")
	if v.Target == nil || v.Target.ID != "target-a" || v.CodingProvider != "claude" || v.AudioProvider != "openai-realtime-via-codex" {
		t.Fatalf("capability contract missing from state: %+v", v)
	}
}

func TestDelegationBlocksUncorrelatedClaudePeer(t *testing.T) {
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "peer-a", Surface: surface.KindClaude, Name: "Peer", Transport: "uds"}}
	s := NewWithTargets(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		return &Target{Session: resolved, Adapter: target}, err
	}, delivery.Dispatcher{})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "select", TargetID: "peer-a"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "delegate", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Do work"}); err == nil || len(target.sent) != 0 {
		t.Fatalf("uncorrelated peer was dispatched: err=%v sends=%v", err, target.sent)
	}
}

func TestSpokenTranscriptDelegatesToSelectedTargetOnce(t *testing.T) {
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Disposable Codex", Transport: "desktop"}}
	s := NewWithTargets(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return &Target{Session: resolved, Adapter: target}, nil
	}, delivery.Dispatcher{})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "select", TargetID: "target-a"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	spoken := Event{Sequence: 27, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Inspect this session"}}
	observeFixture(t, s, p, spoken)
	observeFixture(t, s, p, spoken)
	if !reflect.DeepEqual(target.sent, []string{"Inspect this session"}) {
		t.Fatalf("spoken transcript was not delivered exactly once: %v", target.sent)
	}
}

func TestDynamicVoiceToolsTransferAndReturnThroughSharedState(t *testing.T) {
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Build session", Transport: "desktop"}}
	s := NewWithTargets(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return &Target{Session: resolved, Adapter: target}, nil
	}, delivery.Dispatcher{})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	observeFixture(t, s, p, Event{Method: "item/tool/call", Params: map[string]any{"requestId": "tool-a", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "target-a"}}})
	if got := s.View("phone").Target; got == nil || got.ID != "target-a" {
		t.Fatalf("transfer did not select target: %+v", got)
	}
	p.mu.Lock()
	if len(p.toolCalls) != 1 || !p.toolCalls[0].success || !strings.Contains(p.toolCalls[0].text, "Build session") {
		t.Fatalf("transfer response=%+v", p.toolCalls)
	}
	p.mu.Unlock()
	observeFixture(t, s, p, Event{Method: "item/tool/call", Params: map[string]any{"requestId": "tool-b", "threadId": "operator", "namespace": "agenthail", "tool": "voice_return_to_orchestrator", "arguments": map[string]any{}}})
	if got := s.View("phone").Target; got != nil {
		t.Fatalf("return did not clear target: %+v", got)
	}
	p.mu.Lock()
	if len(p.toolCalls) != 2 || !p.toolCalls[1].success || !strings.Contains(p.toolCalls[1].text, "orchestrator") {
		t.Fatalf("return response=%+v", p.toolCalls)
	}
	p.mu.Unlock()
}

func TestTargetInterruptRequiresConfirmedSelectedTurn(t *testing.T) {
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Build session", Transport: "desktop"}, capabilities: surface.Capabilities{Send: true, Stream: true, Interrupt: true}, observation: &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-a"}, activeTurnID: "turn-a"}
	s := NewWithTargets(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return &Target{Session: resolved, Adapter: target}, nil
	}, delivery.Dispatcher{})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "select", TargetID: "target-a"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, s, Action{Action: "target-interrupt", AttemptID: "call-a"})
	if target.interrupts != 1 || target.interruptTurnID != "turn-a" || !strings.Contains(s.View("phone").Message, "turn-a") {
		t.Fatalf("interrupts=%d state=%+v", target.interrupts, s.View("phone"))
	}
	target.observation = &surface.TurnObservation{Status: surface.StatusBusy}
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "target-interrupt", AttemptID: "call-a"}); err == nil || target.interrupts != 1 {
		t.Fatalf("unconfirmed target was interrupted: err=%v interrupts=%d", err, target.interrupts)
	}
	target.observation = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-b"}
	target.capabilities = surface.Capabilities{Send: true, Stream: true}
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "target-interrupt", AttemptID: "call-a"}); err == nil || target.interrupts != 1 {
		t.Fatalf("unsupported target was interrupted: err=%v interrupts=%d", err, target.interrupts)
	}
	target.capabilities = surface.Capabilities{Send: true, Stream: true, Interrupt: true}
	target.observation = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-a"}
	target.activeTurnID = "turn-b"
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "target-interrupt", AttemptID: "call-a"}); err == nil || target.interrupts != 1 {
		t.Fatalf("replacement turn was interrupted: err=%v interrupts=%d", err, target.interrupts)
	}
}

func TestCorruptStateFailsClosedAndUnknownCreateDoesNotDuplicate(t *testing.T) {
	s, p := fixture(t)
	p.createErr = surface.DeliveryOutcomeUnknown(errors.New("lost creation reply"))
	for i := 0; i < 2; i++ {
		if _, err := s.Apply(context.Background(), "phone", Action{Action: "prepare"}); err == nil {
			t.Fatal("unknown creation accepted")
		}
	}
	if p.creates != 1 {
		t.Fatal("duplicated unknown operator")
	}
	if err := os.WriteFile(s.path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := New(s.path, p, nil, "/fixture/agenthail")
	if broken.View("phone").Phase != "blocked" {
		t.Fatal("corrupt identity was silently replaced")
	}
}

func TestPrepareMigratesPersistedOperatorWithoutDynamicTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice", "operator.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	old := diskState{State: State{Protocol: 1, Session: &surface.Session{ID: "old-operator", Surface: surface.KindCodex, Name: "Old operator", Transport: "desktop"}, Phase: "ready", Events: []Event{}}}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := &fixtureProvider{}
	s := New(path, p, nil, "/fixture/agenthail")
	v := apply(t, s, Action{Action: "prepare"})
	if p.creates != 1 || v.Session == nil || v.Session.ID == "old-operator" || !v.DynamicTools {
		t.Fatalf("persisted operator was not migrated: creates=%d state=%+v", p.creates, v)
	}
	if !strings.Contains(v.Message, "transfer tools") {
		t.Fatalf("migration was not visible: %q", v.Message)
	}
}

func TestPrepareDoesNotMigrateAnUnknownPersistedCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice", "operator.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	old := diskState{State: State{Protocol: 1, Session: &surface.Session{ID: "old-operator", Surface: surface.KindCodex, Name: "Old operator", Transport: "desktop"}, Phase: "connected", AttemptID: "call-a", Events: []Event{}}}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := &fixtureProvider{}
	s := New(path, p, nil, "/fixture/agenthail")
	if _, err := s.Apply(context.Background(), "phone", Action{Action: "prepare"}); err == nil {
		t.Fatal("unknown persisted call was replaced during migration")
	}
	if p.creates != 0 || s.View("phone").Phase != "unknown" {
		t.Fatalf("migration changed unknown call: creates=%d state=%+v", p.creates, s.View("phone"))
	}
}

func TestRealtimeSettingsUseAudioOnlyTargetUpdatesAndNoCredentials(t *testing.T) {
	p := StartParams("operator", "call", "offer", false)
	if p["version"] != "v3" || p["outputModality"] != "audio" {
		t.Fatalf("params=%v", p)
	}
	if _, ok := p["codexResponseHandoffMode"]; ok {
		t.Fatal("legacy realtime handoff remains enabled")
	}
	transport := p["transport"].(map[string]any)
	if transport["type"] != "webrtc" || transport["sdp"] != "offer" {
		t.Fatalf("transport=%v", transport)
	}
	for key := range p {
		if strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "apikey") {
			t.Fatal("credential in voice params")
		}
	}
	initial := p["initialItems"].([]map[string]any)
	if !strings.Contains(initial[0]["text"].(string), "established Agenthail Operations workflow") {
		t.Fatalf("normal orchestrator instructions=%v", initial)
	}
	bound := StartParams("operator", "call", "offer", true)
	boundInitial := bound["initialItems"].([]map[string]any)
	if !strings.Contains(boundInitial[0]["text"].(string), "existing Agenthail coding session") {
		t.Fatalf("session-bound instructions=%v", boundInitial)
	}
}

func TestDelegationHoldsWhenTargetDoesNotProvideAnAuthoritativeTurnID(t *testing.T) {
	p := &fixtureProvider{}
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Codex", Transport: "desktop"}, turnID: "target-a"}
	s := NewWithTargets(filepath.Join(t.TempDir(), "voice", "operator.json"), p, nil, "/fixture/agenthail", func(_ context.Context, id string) (*Target, error) {
		resolved, err := target.Resolve(context.Background(), id)
		if err != nil {
			return nil, err
		}
		return &Target{Session: resolved, Adapter: target}, nil
	}, delivery.Dispatcher{})
	t.Cleanup(func() { s.mu.Lock(); s.state.State.Phase = "ended"; s.mu.Unlock() })
	apply(t, s, Action{Action: "prepare"})
	apply(t, s, Action{Action: "select", TargetID: "target-a"})
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, s, Action{Action: "delegate", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect the target"})
	if got := s.View("phone").Message; !strings.Contains(got, "authoritative turn ID") {
		t.Fatalf("missing correlation hold: %q", got)
	}
}

func TestCompletedAnswersAreNotReinjectedAsSessionSpeech(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, s, Action{Action: "connected", AttemptID: "call-a"})
	observeFixture(t, s, p, Event{Method: "turn/started", Params: map[string]any{"turn": map[string]any{"id": "current-turn"}}})
	observeFixture(t, s, p, Event{Method: "item/completed", Params: map[string]any{"turnId": "current-turn", "item": map[string]any{"id": "final", "type": "agentMessage", "phase": "final_answer", "text": "The selected task replied."}}})
	if len(p.requests) != 1 || p.requests[0] != "thread/realtime/start" {
		t.Fatalf("completed answer was duplicated through a manual request: %v", p.requests)
	}
}

func TestDisconnectedPhoneWatchdogRequestsAudioStopWithoutDeadlock(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	s.mu.Lock()
	s.lastSeen = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("watchdog did not request hangup")
		case <-ticker.C:
			p.mu.Lock()
			stopped := len(p.requests) == 2 && p.requests[1] == "thread/realtime/stop" && p.interrupts == 0
			p.mu.Unlock()
			if stopped {
				done := make(chan State, 1)
				go func() { done <- s.View("phone") }()
				select {
				case v := <-done:
					if v.Phase != "stopping" {
						t.Fatalf("hangup not represented: %+v", v)
					}
					return
				case <-deadline:
					t.Fatal("watchdog retained service lock")
				}
			}
		}
	}
}

func TestSDPRequiresMatchingStartAndEventLossPreventsReattachment(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	observeFixture(t, s, p, Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "old-call"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 old"}})
	if s.View("phone").SDP != "" {
		t.Fatal("accepted an unbound SDP")
	}
	p.mu.Lock()
	p.batch = Batch{Lost: true, Cursor: Cursor{Source: "replacement", Position: 2}, Events: []Event{{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, {Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 untrusted"}}}}
	p.mu.Unlock()
	s.mu.Lock()
	err := s.poll(context.Background())
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if v := s.View("phone"); v.Phase != "unknown" || v.SDP != "" || !v.Truncated {
		t.Fatalf("loss not surfaced: %+v", v)
	}
}

func TestPersistenceFailureDoesNotPreventAudioTeardown(t *testing.T) {
	s, p := fixture(t)
	startFixture(t, s)
	s.mu.Lock()
	s.path = filepath.Join(s.path, "invalid-child")
	s.loadErr = errors.New("disk unavailable")
	s.mu.Unlock()
	_, err := s.Apply(context.Background(), "phone", Action{Action: "stop", AttemptID: "call-a"})
	if err == nil {
		t.Fatal("persistence failure hidden")
	}
	if len(p.requests) != 2 || p.requests[1] != "thread/realtime/stop" {
		t.Fatalf("audio teardown skipped: %v", p.requests)
	}
}
