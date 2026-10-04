package voice

import (
	"context"
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
	pending      []Batch
	polled       chan struct{}
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
func (p *fixtureProvider) Poll(_ context.Context, after Cursor, _ string) (Batch, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := Batch{Cursor: after}
	if len(p.pending) > 0 {
		b, p.pending = p.pending[0], p.pending[1:]
	}
	select {
	case p.polled <- struct{}{}:
	default:
	}
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

func (p *fixtureProvider) failRequests(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *fixtureProvider) failCreate(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.createErr = err
}

func (p *fixtureProvider) snapshot() (requests []string, interrupts, creates int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...), p.interrupts, p.creates
}

func (p *fixtureProvider) spoken() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var texts []string
	for _, params := range p.params {
		if text, ok := params["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	return texts
}

type fixtureOptions struct {
	register       func(surface.Session) error
	target         *targetFixture
	operatorSource func(*surface.Session, bool)
	stream         SessionStreamProvider
}

type voiceFixture struct {
	t        *testing.T
	service  *Service
	provider *fixtureProvider
	path     string
	ticks    chan time.Time
	mu       sync.Mutex
	now      time.Time
}

func fixture(t *testing.T) (*voiceFixture, *fixtureProvider) {
	f := newFixture(t, fixtureOptions{})
	return f, f.provider
}

func newFixture(t *testing.T, options fixtureOptions) *voiceFixture {
	t.Helper()
	f := &voiceFixture{t: t, provider: &fixtureProvider{polled: make(chan struct{}, 1)}, path: filepath.Join(t.TempDir(), "voice", "operator.json"), ticks: make(chan time.Time), now: time.Unix(1_700_000_000, 0)}
	var resolver TargetResolver
	if target := options.target; target != nil {
		resolver = func(_ context.Context, id string) (*Target, error) {
			resolved, err := target.Resolve(context.Background(), id)
			if err != nil {
				return nil, err
			}
			return &Target{Session: resolved, Adapter: target}, nil
		}
	}
	manual := clock{
		now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.now
		},
		ticker: func() (<-chan time.Time, func()) { return f.ticks, func() {} },
	}
	f.service = newService(f.path, f.provider, options.register, "/fixture/agenthail", resolver, delivery.Dispatcher{}, options.operatorSource, options.stream, manual)
	t.Cleanup(func() { close(f.ticks) })
	return f
}

func (f *voiceFixture) Apply(a Action) (State, error) {
	return f.service.Apply(context.Background(), "phone", a)
}

func (f *voiceFixture) View() State { return f.service.View("phone") }

func (f *voiceFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *voiceFixture) tick() {
	f.t.Helper()
	select {
	case f.ticks <- time.Time{}:
	case <-time.After(time.Second):
		f.t.Fatal("call observer is not running")
	}
	select {
	case <-f.provider.polled:
	case <-time.After(time.Second):
		f.t.Fatal("call observer did not poll")
	}
	// View takes the service lock, so the cycle that polled has completed.
	done := make(chan struct{})
	go func() { f.service.View(""); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		f.t.Fatal("call observer retained the service lock")
	}
}

func (f *voiceFixture) observe(events ...Event) {
	f.t.Helper()
	f.provider.mu.Lock()
	f.provider.pending = append(f.provider.pending, Batch{Cursor: Cursor{Source: "fixture", Position: 20}, Events: events})
	f.provider.mu.Unlock()
	f.tick()
}

func (f *voiceFixture) loseEvents(events ...Event) {
	f.t.Helper()
	f.provider.mu.Lock()
	f.provider.pending = append(f.provider.pending, Batch{Lost: true, Cursor: Cursor{Source: "replacement", Position: 2}, Events: events})
	f.provider.mu.Unlock()
	f.tick()
}

func apply(t *testing.T, f *voiceFixture, a Action) State {
	t.Helper()
	v, err := f.Apply(a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func startFixture(t *testing.T, f *voiceFixture) {
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "start", AttemptID: "call-a", SDP: "v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n"})
}

func connectFixture(t *testing.T, f *voiceFixture) {
	startFixture(t, f)
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, f, Action{Action: "connected", AttemptID: "call-a"})
}

func hangUp(t *testing.T, f *voiceFixture) {
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	f.observe(Event{Method: "thread/realtime/closed", Params: map[string]any{}})
}

func targetSelected(t *testing.T, target *targetFixture) *voiceFixture {
	f := newFixture(t, fixtureOptions{target: target})
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "select", TargetID: target.session.ID})
	connectFixture(t, f)
	return f
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hasEventWith(v State, method, key, value string) bool {
	for _, event := range v.Events {
		if event.Method == method && event.Params[key] == value {
			return true
		}
	}
	return false
}

func hasEvent(v State, method string) bool {
	for _, event := range v.Events {
		if event.Method == method {
			return true
		}
	}
	return false
}

func TestPersistentOperatorLoadsLiteralSkillWithoutStartingTurn(t *testing.T) {
	f, p := fixture(t)
	first := apply(t, f, Action{Action: "prepare"})
	second := apply(t, f, Action{Action: "prepare"})
	requests, _, creates := p.snapshot()
	if first.Session.ID != second.Session.ID || creates != 1 || len(requests) != 0 {
		t.Fatalf("duplicate operator or startup turn: creates=%d requests=%v", creates, requests)
	}
	if p.instructions != OperatorInstructions("/fixture/agenthail") || first.SkillDigest == "" {
		t.Fatal("operator did not receive the packaged skill contract")
	}
	reopened := New(f.path, p, nil, "/fixture/agenthail")
	if v, err := reopened.Apply(context.Background(), "phone", Action{Action: "prepare"}); err != nil || v.Session.ID != "operator" || p.creates != 1 {
		t.Fatalf("resume v=%+v err=%v creates=%d", v, err, p.creates)
	}
	info, err := os.Stat(f.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private state: %v %v", info, err)
	}
}

func TestNewOperatorCreatesDistinctPersistentThreadAndRetainsOldThread(t *testing.T) {
	var registered []string
	f := newFixture(t, fixtureOptions{register: func(session surface.Session) error {
		registered = append(registered, session.ID)
		return nil
	}})
	connectFixture(t, f)
	hangUp(t, f)
	first := f.View()
	if len(first.Events) == 0 {
		t.Fatal("call left no conversation events to clear")
	}
	second := apply(t, f, Action{Action: "new"})
	if first.Session.ID == second.Session.ID || second.Session.ID != "operator-2" || f.provider.creates != 2 {
		t.Fatalf("new operator was not distinct: first=%+v second=%+v creates=%d", first.Session, second.Session, f.provider.creates)
	}
	if len(second.Events) != 0 || second.Phase != "ready" {
		t.Fatalf("new operator inherited prior conversation state: %+v", second)
	}
	if !reflect.DeepEqual(registered, []string{"operator", "operator-2"}) {
		t.Fatalf("old and new operators were not both registered: %v", registered)
	}
	reopened := New(f.path, f.provider, nil, "/fixture/agenthail")
	if got := reopened.View("phone"); got.Session == nil || got.Session.ID != "operator-2" || len(got.Events) != 0 {
		t.Fatalf("new operator identity was not durable: %+v", got)
	}
}

func TestNewOperatorFailsClosedAcrossActiveAndUnknownCreation(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	first := f.View()
	if _, err := f.Apply(Action{Action: "new"}); err == nil || p.creates != 1 {
		t.Fatal("active call allowed operator replacement")
	}
	hangUp(t, f)
	p.failCreate(surface.DeliveryOutcomeUnknown(errors.New("lost replacement reply")))
	if got, err := f.Apply(Action{Action: "new"}); err == nil || got.Session.ID != first.Session.ID || got.Phase != "creating" {
		t.Fatalf("unknown replacement discarded the inspectable prior thread: got=%+v err=%v", got, err)
	}
	if _, err := f.Apply(Action{Action: "new"}); err == nil || p.creates != 2 {
		t.Fatal("unknown replacement was retried")
	}
}

func TestCallLifecycleIsAudioSeparateFromWork(t *testing.T) {
	f, p := fixture(t)
	startFixture(t, f)
	if v := f.View(); v.Phase != "starting" || v.SDP != "" {
		t.Fatalf("accepted RPC is not connected: %+v", v)
	}
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}})
	if f.View().Phase != "starting" {
		t.Fatal("started event is not an SDP answer")
	}
	f.observe(Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	if v := f.View(); v.Phase != "negotiating" || v.SDP != "v=0 answer" {
		t.Fatalf("answer was not exposed: %+v", v)
	}
	apply(t, f, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	if _, interrupts, _ := p.snapshot(); f.View().Phase != "stopping" || interrupts != 0 {
		t.Fatal("hangup must await closure and not interrupt agent work")
	}
	f.observe(Event{Method: "thread/realtime/closed", Params: map[string]any{}})
	apply(t, f, Action{Action: "start", AttemptID: "call-b", SDP: "v=0 offer-b"})
	if requests, _, creates := p.snapshot(); creates != 1 || !reflect.DeepEqual(requests, []string{"thread/realtime/start", "thread/realtime/stop", "thread/realtime/start"}) {
		t.Fatalf("flow: %+v", requests)
	}
	apply(t, f, Action{Action: "interrupt"})
	if _, interrupts, _ := p.snapshot(); interrupts != 1 {
		t.Fatal("explicit interrupt did not use the native operation")
	}
}

func TestUnknownStartupIsNotReplayed(t *testing.T) {
	f, p := fixture(t)
	apply(t, f, Action{Action: "prepare"})
	p.failRequests(errors.New("response lost"))
	a := Action{Action: "start", AttemptID: "call-a", SDP: "v=0 offer"}
	if v, err := f.Apply(a); err == nil || v.Phase != "unknown" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	apply(t, f, a)
	if requests, _, _ := p.snapshot(); len(requests) != 1 {
		t.Fatal("unknown startup was replayed")
	}
	if _, err := f.Apply(Action{Action: "start", AttemptID: "call-b", SDP: "v=0 new"}); err == nil {
		t.Fatal("replaced an unconfirmed call")
	}
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 delayed answer"}})
	if f.View().Phase != "negotiating" {
		t.Fatal("late acceptance did not reconcile")
	}
	reopened := New(f.path, p, nil, "/fixture/agenthail")
	if reopened.View("phone").Phase != "unknown" || reopened.View("phone").SDP != "" {
		t.Fatal("restart pretended to retain a physical audio connection")
	}
}

func TestDeviceLeaseRejectsTakeoverAndHidesSDP(t *testing.T) {
	f, _ := fixture(t)
	startFixture(t, f)
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 private"}})
	if v := f.service.View("other-phone"); !v.Occupied || v.SDP != "" {
		t.Fatalf("leaked call: %+v", v)
	}
	for _, action := range []string{"prepare", "start", "connected", "text", "stop", "interrupt"} {
		if _, err := f.service.Apply(context.Background(), "other-phone", Action{Action: action, AttemptID: "call-a"}); err == nil {
			t.Fatalf("%s takeover accepted", action)
		}
	}
}

func TestOperatorSourceLifecycleTracksOnlyActiveTransitions(t *testing.T) {
	var calls []string
	f := newFixture(t, fixtureOptions{operatorSource: func(session *surface.Session, active bool) {
		calls = append(calls, session.ID+":"+strconv.FormatBool(active))
	}})
	connectFixture(t, f)
	hangUp(t, f)
	if !reflect.DeepEqual(calls, []string{"operator:true", "operator:false"}) {
		t.Fatalf("operator source calls=%v", calls)
	}
}

func TestTextUnknownReceiptPreventsDuplicate(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	p.failRequests(errors.New("response lost"))
	a := Action{Action: "text", AttemptID: "call-a", MessageID: "message-a", Text: "Please inspect my agents"}
	v, err := f.Apply(a)
	if err == nil || v.TextReceipt != "unknown:message-a" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if _, err = f.Apply(a); err == nil {
		t.Fatal("duplicate text accepted")
	}
	if requests, _, _ := p.snapshot(); len(requests) != 2 {
		t.Fatalf("unknown text was replayed: %v", requests)
	}
}

func TestDelegationUsesSelectedTargetAndOnlySpeaksCorrelatedTurn(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Disposable Claude", Transport: "browser"}}
	spoken := make(chan struct{})
	target.stream = func(callback func(surface.StreamEvent)) {
		callback(surface.StreamEvent{Kind: "text", Text: "The correlated answer."})
		callback(surface.StreamEvent{ID: "partial-upsert", Operation: "upsert", Kind: "text", Text: "The partial answer."})
		callback(surface.StreamEvent{ID: "final-item", Operation: "upsert", Final: true, Kind: "text", Text: "The authoritative answer."})
		callback(surface.StreamEvent{ID: "final-item", Operation: "upsert", Final: true, Kind: "text", Text: "The authoritative answer."})
		close(spoken)
	}
	f := targetSelected(t, target)
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect the current failure"})
	select {
	case <-spoken:
	case <-time.After(time.Second):
		t.Fatal("correlated target stream did not reach the audio handoff")
	}
	if !reflect.DeepEqual(target.sent, []string{"Inspect the current failure"}) {
		t.Fatalf("wrong target dispatch: %v", target.sent)
	}
	want := []string{"The correlated answer.", "The partial answer.", "The authoritative answer."}
	if got := f.provider.spoken(); !reflect.DeepEqual(got, want) {
		t.Fatalf("spoken=%q want=%q", got, want)
	}
	v := f.View()
	if v.Target == nil || v.Target.ID != "target-a" || v.CodingProvider != "claude" || v.AudioProvider != "openai-realtime-via-codex" {
		t.Fatalf("capability contract missing from state: %+v", v)
	}
}

func TestDelegationEmptyButCompletedTurnIsNotFailure(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Disposable Claude", Transport: "browser"}}
	streamed := make(chan struct{})
	target.stream = func(callback func(surface.StreamEvent)) {
		callback(surface.StreamEvent{ID: "codex:target-turn:done", Operation: "phase", TurnID: "target-turn", Kind: "done"})
		close(streamed)
	}
	f := targetSelected(t, target)
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Run the tool only"})
	select {
	case <-streamed:
	case <-time.After(time.Second):
		t.Fatal("tool-only target stream did not complete")
	}
	time.Sleep(100 * time.Millisecond)
	v := f.View()
	if hasEvent(v, "voice/delegation/failed") {
		t.Fatalf("completed tool-only turn surfaced a delegation failure: %+v", v.Events)
	}
	if spoken := f.provider.spoken(); len(spoken) != 0 {
		t.Fatalf("empty turn handed text to realtime audio: %q", spoken)
	}
}

func TestDirectDelegationNormalizesClaudeMessagesPerItem(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Claude", Transport: "browser"}}
	target.stream = func(callback func(surface.StreamEvent)) {
		callback(surface.StreamEvent{ID: "user", Role: "user", Kind: "message", Text: "do not speak"})
		callback(surface.StreamEvent{ID: "call", Role: "assistant", Kind: "toolCall", Text: "Read x"})
		callback(surface.StreamEvent{ID: "answer", Role: "assistant", Operation: "append", Version: 4, Kind: "text", Text: "part"})
		callback(surface.StreamEvent{ID: "answer", Role: "assistant", Operation: "upsert", Version: 13, Final: true, Kind: "message", Text: "part complete"})
		callback(surface.StreamEvent{ID: "second", Role: "assistant", Final: true, Kind: "message", Text: "second answer"})
		callback(surface.StreamEvent{Kind: "done"})
	}
	f := targetSelected(t, target)
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Run the task"})
	waitFor(t, "three spoken items", func() bool { return len(f.provider.spoken()) >= 3 })
	if spoken := f.provider.spoken(); !reflect.DeepEqual(spoken, []string{"part", " complete", "second answer"}) {
		t.Fatalf("spoken=%q", spoken)
	}
}

func TestDirectDelegationFailsOnCancelledTerminal(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindClaude, Name: "Claude", Transport: "browser"}}
	target.stream = func(callback func(surface.StreamEvent)) {
		callback(surface.StreamEvent{ID: "answer", Role: "assistant", Final: true, Kind: "message", Text: "partial"})
		callback(surface.StreamEvent{Kind: "done", Status: "cancelled"})
	}
	f := targetSelected(t, target)
	apply(t, f, Action{Action: "text", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Run the task"})
	waitFor(t, "delegation failure", func() bool { return hasEvent(f.View(), "voice/delegation/failed") })
}

func TestDelegationBlocksUncorrelatedClaudePeer(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "peer-a", Surface: surface.KindClaude, Name: "Peer", Transport: "uds"}}
	f := targetSelected(t, target)
	if _, err := f.Apply(Action{Action: "delegate", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Do work"}); err == nil || len(target.sent) != 0 {
		t.Fatalf("uncorrelated peer was dispatched: err=%v sends=%v", err, target.sent)
	}
}

func TestSpokenTranscriptDelegatesToSelectedTargetOnce(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Disposable Codex", Transport: "desktop"}}
	f := targetSelected(t, target)
	spoken := Event{Sequence: 27, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Inspect this session"}}
	f.observe(spoken)
	f.observe(spoken)
	if !reflect.DeepEqual(target.sent, []string{"Inspect this session"}) {
		t.Fatalf("spoken transcript was not delivered exactly once: %v", target.sent)
	}
}

func TestDynamicVoiceToolsTransferAndReturnThroughSharedState(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Build session", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target})
	p := f.provider
	connectFixture(t, f)
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "tool-a", "threadId": "operator", "namespace": "agenthail", "tool": "voice_transfer", "arguments": map[string]any{"targetId": "target-a"}}})
	if got := f.View().Target; got == nil || got.ID != "target-a" {
		t.Fatalf("transfer did not select target: %+v", got)
	}
	p.mu.Lock()
	if len(p.toolCalls) != 1 || !p.toolCalls[0].success || p.toolCalls[0].requestID != "tool-a" || !strings.Contains(p.toolCalls[0].text, "Build session") {
		t.Fatalf("transfer response=%+v", p.toolCalls)
	}
	p.mu.Unlock()
	f.observe(Event{Method: "item/tool/call", Params: map[string]any{"requestId": "tool-b", "threadId": "operator", "namespace": "agenthail", "tool": "voice_return_to_orchestrator", "arguments": map[string]any{}}})
	if got := f.View().Target; got != nil {
		t.Fatalf("return did not clear target: %+v", got)
	}
	p.mu.Lock()
	if len(p.toolCalls) != 2 || !p.toolCalls[1].success || p.toolCalls[1].requestID != "tool-b" {
		t.Fatalf("return response=%+v", p.toolCalls)
	}
	p.mu.Unlock()
}

func TestTargetInterruptRequiresConfirmedSelectedTurn(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Build session", Transport: "desktop"}, capabilities: surface.Capabilities{Send: true, Stream: true, Interrupt: true}, observation: &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-a"}, activeTurnID: "turn-a"}
	f := targetSelected(t, target)
	apply(t, f, Action{Action: "target-interrupt", AttemptID: "call-a"})
	if v := f.View(); target.interrupts != 1 || target.interruptTurnID != "turn-a" || !hasEventWith(v, "voice/target/interrupt", "turnId", "turn-a") {
		t.Fatalf("interrupts=%d state=%+v", target.interrupts, v)
	}
	target.observation = &surface.TurnObservation{Status: surface.StatusBusy}
	if _, err := f.Apply(Action{Action: "target-interrupt", AttemptID: "call-a"}); err == nil || target.interrupts != 1 {
		t.Fatalf("unconfirmed target was interrupted: err=%v interrupts=%d", err, target.interrupts)
	}
	target.observation = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-b"}
	target.capabilities = surface.Capabilities{Send: true, Stream: true}
	if _, err := f.Apply(Action{Action: "target-interrupt", AttemptID: "call-a"}); err == nil || target.interrupts != 1 {
		t.Fatalf("unsupported target was interrupted: err=%v interrupts=%d", err, target.interrupts)
	}
	target.capabilities = surface.Capabilities{Send: true, Stream: true, Interrupt: true}
	target.observation = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-a"}
	target.activeTurnID = "turn-b"
	if _, err := f.Apply(Action{Action: "target-interrupt", AttemptID: "call-a"}); err == nil || target.interrupts != 1 {
		t.Fatalf("replacement turn was interrupted: err=%v interrupts=%d", err, target.interrupts)
	}
}

func TestCorruptStateFailsClosedAndUnknownCreateDoesNotDuplicate(t *testing.T) {
	f, p := fixture(t)
	p.failCreate(surface.DeliveryOutcomeUnknown(errors.New("lost creation reply")))
	for i := 0; i < 2; i++ {
		if _, err := f.Apply(Action{Action: "prepare"}); err == nil {
			t.Fatal("unknown creation accepted")
		}
	}
	if p.creates != 1 {
		t.Fatal("duplicated unknown operator")
	}
	if err := os.WriteFile(f.path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := New(f.path, p, nil, "/fixture/agenthail")
	if broken.View("phone").Phase != "blocked" {
		t.Fatal("corrupt identity was silently replaced")
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
	boundInitial := StartParams("operator", "call", "offer", true)["initialItems"].([]map[string]any)
	orchestrator, _ := initial[0]["text"].(string)
	sessionBound, _ := boundInitial[0]["text"].(string)
	if len(initial) != 1 || len(boundInitial) != 1 || initial[0]["role"] != "developer" || boundInitial[0]["role"] != "developer" || orchestrator == "" || sessionBound == "" || orchestrator == sessionBound {
		t.Fatalf("session-bound calls need their own developer instructions: orchestrator=%v bound=%v", initial, boundInitial)
	}
}

func TestDelegationHoldsWhenTargetDoesNotProvideAnAuthoritativeTurnID(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Codex", Transport: "desktop"}, turnID: "target-a"}
	f := targetSelected(t, target)
	apply(t, f, Action{Action: "delegate", AttemptID: "call-a", MessageID: "voice-request-a", Text: "Inspect the target"})
	if v := f.View(); !hasEventWith(v, "voice/delegation/held", "turnId", "target-a") || hasEvent(v, "voice/delegation/dispatched") {
		t.Fatalf("uncorrelated receipt was not held: %+v", v.Events)
	}
	if spoken := f.provider.spoken(); len(spoken) != 0 {
		t.Fatalf("held delegation spoke: %q", spoken)
	}
}

func TestCompletedAnswersAreNotReinjectedAsSessionSpeech(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	f.observe(Event{Method: "turn/started", Params: map[string]any{"turn": map[string]any{"id": "current-turn"}}})
	f.observe(Event{Method: "item/completed", Params: map[string]any{"turnId": "current-turn", "item": map[string]any{"id": "final", "type": "agentMessage", "phase": "final_answer", "text": "The selected task replied."}}})
	if requests, _, _ := p.snapshot(); len(requests) != 1 || requests[0] != "thread/realtime/start" {
		t.Fatalf("completed answer was duplicated through a manual request: %v", requests)
	}
}

func TestDisconnectedPhoneWatchdogRequestsAudioStopWithoutDeadlock(t *testing.T) {
	f, p := fixture(t)
	startFixture(t, f)
	f.advance(39 * time.Second)
	f.tick()
	if requests, _, _ := p.snapshot(); len(requests) != 1 {
		t.Fatalf("watchdog fired before the phone timeout: %v", requests)
	}
	f.advance(2 * time.Second)
	f.tick()
	requests, interrupts, _ := p.snapshot()
	if !reflect.DeepEqual(requests, []string{"thread/realtime/start", "thread/realtime/stop"}) || interrupts != 0 {
		t.Fatalf("watchdog requests=%v interrupts=%d", requests, interrupts)
	}
	if v := f.View(); v.Phase != "stopping" {
		t.Fatalf("hangup not represented: %+v", v)
	}
}

func TestSDPRequiresMatchingStartAndEventLossPreventsReattachment(t *testing.T) {
	f, _ := fixture(t)
	startFixture(t, f)
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "old-call"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 old"}})
	if f.View().SDP != "" {
		t.Fatal("accepted an unbound SDP")
	}
	f.loseEvents(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 untrusted"}})
	if v := f.View(); v.Phase != "unknown" || v.SDP != "" || !v.Truncated {
		t.Fatalf("loss not surfaced: %+v", v)
	}
}

func TestPersistenceFailureDoesNotPreventAudioTeardown(t *testing.T) {
	f, p := fixture(t)
	startFixture(t, f)
	dir := filepath.Dir(f.path)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}})
	if v := f.View(); v.Phase != "blocked" {
		t.Fatalf("persistence failure hidden: %+v", v)
	}
	if _, err := f.Apply(Action{Action: "text", AttemptID: "call-a", MessageID: "m", Text: "hello"}); err == nil {
		t.Fatal("non-teardown action accepted with unpersistable state")
	}
	if _, err := f.Apply(Action{Action: "stop", AttemptID: "call-a"}); err == nil {
		t.Fatal("persistence failure hidden from hangup")
	}
	if requests, _, _ := p.snapshot(); !reflect.DeepEqual(requests, []string{"thread/realtime/start", "thread/realtime/stop"}) {
		t.Fatalf("audio teardown skipped: %v", requests)
	}
}
