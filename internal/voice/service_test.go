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
	toolErr      error
	pending      []Batch
	polled       chan struct{}
	interrupts   int
	speaking     chan struct{}
	speakingText string
	toolCalls    []struct {
		requestID string
		success   bool
		text      string
	}
}

type targetFixture struct {
	session         surface.Session
	sent            []string
	steered         []string
	stream          func(func(surface.StreamEvent))
	turnID          string
	observation     *surface.TurnObservation
	interrupts      int
	interruptTurnID string
	activeTurnID    string
	capabilities    surface.Capabilities
	watchDone       chan struct{}
	streaming       chan struct{}
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
func (f *targetFixture) Stream(ctx context.Context, _ *surface.Session, turnID string, callback func(surface.StreamEvent), _ time.Duration) error {
	if f.watchDone != nil {
		go func() { <-ctx.Done(); close(f.watchDone) }()
	}
	if turnID != "target-turn" {
		return errors.New("wrong turn")
	}
	if f.streaming != nil {
		close(f.streaming)
		<-ctx.Done()
		return ctx.Err()
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
func (f *targetFixture) Steer(_ context.Context, _ *surface.Session, text string) error {
	f.steered = append(f.steered, text)
	return nil
}
func (f *targetFixture) Capabilities() surface.Capabilities {
	if f.capabilities.Send || f.capabilities.Interrupt || f.capabilities.Steer {
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

func (p *fixtureProvider) RespondDynamicToolCall(_ context.Context, requestID, threadID string, success bool, text string) error {
	if threadID != "operator" && !strings.HasPrefix(threadID, "operator-") {
		return errors.New("unexpected tool response thread")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolCalls = append(p.toolCalls, struct {
		requestID string
		success   bool
		text      string
	}{requestID, success, text})
	return p.toolErr
}
func (p *fixtureProvider) Request(ctx context.Context, _ *surface.Session, method string, params map[string]any) error {
	p.mu.Lock()
	speaking, speakingText := p.speaking, p.speakingText
	p.mu.Unlock()
	text, _ := params["text"].(string)
	if speaking != nil && method == "thread/realtime/appendText" && strings.Contains(text, speakingText) {
		close(speaking)
		<-ctx.Done()
		return ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, method)
	p.params = append(p.params, params)
	return p.err
}

func (p *fixtureProvider) StartCall(ctx context.Context, call CallRequest) (string, error) {
	err := p.Request(ctx, nil, "voice/call/start", map[string]any{"prompt": call.Instructions, "sdp": call.SDP})
	return "v=0 answer", err
}

func (p *fixtureProvider) Execute(ctx context.Context, session *surface.Session, text string) (string, error) {
	return "executor-turn", p.Request(ctx, session, "turn/start", map[string]any{"text": text})
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
	t.Cleanup(f.service.Close)
	return f
}

func (f *voiceFixture) Apply(a Action) (State, error) {
	if a.Action == "start" && a.ContextTransport == "" {
		a.ContextTransport = "native-delegation"
	}
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
	// Taking the service lock waits for the cycle that polled to complete.
	done := make(chan struct{})
	go func() { f.service.mu.Lock(); f.service.unlock(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		f.t.Fatal("call observer retained the service lock")
	}
}

func (f *voiceFixture) observe(events ...Event) {
	f.t.Helper()
	for i, event := range events {
		if event.Method == "item/tool/call" {
			events[i] = f.ownTool(event)
		}
	}
	expected := len(f.View().Events)
	for _, event := range events {
		if !strings.HasPrefix(event.Method, "thread/realtime/") {
			expected++
		}
	}
	if expected > 100 {
		expected = 100
	}
	f.provider.mu.Lock()
	f.provider.pending = append(f.provider.pending, Batch{Cursor: Cursor{Source: "fixture", Position: 20}, Events: events})
	f.provider.mu.Unlock()
	f.tick()
	deadline := time.Now().Add(time.Second)
	for len(f.View().Events) < expected {
		if time.Now().After(deadline) {
			f.t.Fatal("observer did not finish the event batch")
		}
		time.Sleep(time.Millisecond)
	}
	for _, event := range events {
		if event.Method == "item/tool/call" {
			waitFor(f.t, "tool completion", func() bool {
				f.service.mu.Lock()
				defer f.service.mu.Unlock()
				return len(f.service.toolInFlight) == 0
			})
		}
	}
}

func (f *voiceFixture) ownTool(event Event) Event {
	f.service.mu.Lock()
	defer f.service.unlock()
	turn, _ := event.Params["turnId"].(string)
	if turn == "" {
		turn = "fixture-tool-" + event.Params["requestId"].(string)
		event.Params["turnId"] = turn
	}
	if f.service.delegations == nil {
		f.service.delegations = map[string]*delegation{}
	}
	if f.service.turnDelegations == nil {
		f.service.turnDelegations = map[string]string{}
	}
	if f.service.turnDelegations[turn] == "" {
		f.service.turnDelegations[turn] = turn
		f.service.delegations[turn] = &delegation{id: turn, turn: turn, attempt: f.service.state.State.AttemptID, generation: f.service.targetGeneration, items: map[string]bool{}}
	}
	return event
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
	apply(t, f, Action{Action: "connected", AttemptID: "call-a", BindingGeneration: f.View().BindingGeneration})
}

func TestCredentialFailureDoesNotClaimUnknownCallStartup(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	apply(t, f, Action{Action: "prepare"})
	f.provider.failRequests(&CallNotStartedError{Cause: errors.New("Existing Codex voice credential unavailable")})
	_, err := f.Apply(Action{Action: "start", AttemptID: "call-a", SDP: "v=0 offer"})
	v := f.View()
	if err == nil || v.Phase != "ended" || strings.Contains(v.Message, "unknown") || v.SDP != "" {
		t.Fatal("credential failure reported ambiguous audio startup")
	}
}

func hangUp(t *testing.T, f *voiceFixture) {
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
}

func targetSelected(t *testing.T, target *targetFixture) *voiceFixture {
	f := newFixture(t, fixtureOptions{target: target, stream: newJournalFixture()})
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
	f.service.mu.Lock()
	f.service.recordNativeTranscript("assistant", "Hello")
	f.service.unlock()
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
	if v := f.View(); v.Phase != "negotiating" || v.SDP != "v=0 answer" {
		t.Fatalf("accepted RPC is not connected: %+v", v)
	}
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "call-a"}})
	if f.View().Phase != "negotiating" {
		t.Fatal("legacy sideband event changed call state")
	}
	f.observe(Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	if v := f.View(); v.Phase != "negotiating" || v.SDP != "v=0 answer" {
		t.Fatalf("answer was not exposed: %+v", v)
	}
	apply(t, f, Action{Action: "connected", AttemptID: "call-a"})
	apply(t, f, Action{Action: "stop", AttemptID: "call-a"})
	if _, interrupts, _ := p.snapshot(); f.View().Phase != "ended" || interrupts != 0 {
		t.Fatal("hangup must end the phone conversation without interrupting agent work")
	}
	apply(t, f, Action{Action: "start", AttemptID: "call-b", SDP: "v=0 offer-b"})
	if requests, _, creates := p.snapshot(); creates != 1 || !reflect.DeepEqual(requests, []string{"voice/call/start", "voice/call/start"}) {
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
	if f.View().Phase != "unknown" {
		t.Fatal("legacy sideband events reconciled a different direct call")
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

func TestNativeCallHeartbeatSurvivesWithoutViewsAndRejectsWrongIdentity(t *testing.T) {
	f, p := fixture(t)
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "start", AttemptID: "native-a", SDP: "v=0 offer", HeartbeatMode: "explicit"})
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "native-a"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 answer"}})
	apply(t, f, Action{Action: "connected", AttemptID: "native-a"})
	for range 12 {
		f.advance(10 * time.Second)
		apply(t, f, Action{Action: "heartbeat", AttemptID: "native-a"})
		f.tick()
	}
	if got := f.service.published.Load().state.Phase; got != "connected" {
		t.Fatalf("healthy native call ended: %s", got)
	}
	last := f.service.lastSeen.Load()
	f.advance(30 * time.Second)
	for _, tc := range []struct{ name, owner, attempt string }{
		{"other owner", "other-phone", "native-a"}, {"old attempt", "phone", "old"}, {"missing attempt", "phone", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.service.Apply(context.Background(), tc.owner, Action{Action: "heartbeat", AttemptID: tc.attempt}); err == nil {
				t.Fatal("invalid heartbeat accepted")
			}
			if f.service.lastSeen.Load() != last {
				t.Fatal("invalid identity extended the lease")
			}
		})
	}
	f.View()
	if f.service.lastSeen.Load() != last {
		t.Fatal("transcript viewing extended native media lease")
	}
	f.advance(11 * time.Second)
	f.tick()
	requests, interrupts, _ := p.snapshot()
	if requests[len(requests)-1] != "voice/call/start" || interrupts != 0 || f.View().Phase != "ended" {
		t.Fatalf("expiry affected coding work or did not stop audio: %v / %d", requests, interrupts)
	}
}

func TestNativeHeartbeatCannotReviveRetainedCallAfterRestart(t *testing.T) {
	f, _ := fixture(t)
	apply(t, f, Action{Action: "prepare"})
	apply(t, f, Action{Action: "start", AttemptID: "native-a", SDP: "v=0 offer", HeartbeatMode: "explicit"})
	copy := New(f.path, f.provider, nil, "/fixture/agenthail")
	defer copy.Close()
	if got := copy.View("phone"); got.Phase != "unknown" {
		t.Fatalf("restart restored media without evidence: %s", got.Phase)
	}
	if _, err := copy.Apply(context.Background(), "phone", Action{Action: "heartbeat", AttemptID: "native-a"}); err == nil {
		t.Fatal("heartbeat reattached a dead native peer")
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

func TestTypedRequestFailureDoesNotRepeatExecutorSubmission(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	p.failRequests(errors.New("response lost"))
	a := Action{Action: "text", AttemptID: "call-a", MessageID: "message-a", Text: "Please inspect my agents"}
	v, err := f.Apply(a)
	if err != nil || v.TextReceipt != "submitted:message-a" {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	if _, err = f.Apply(a); err == nil {
		t.Fatal("duplicate text accepted")
	}
	waitFor(t, "failed typed result", func() bool { return len(f.View().ContextFrames) == 1 })
	if requests, _, _ := p.snapshot(); len(requests) != 2 || requests[1] != "turn/start" {
		t.Fatalf("typed request must submit exactly one executor turn: %v", requests)
	}
}

func TestSpokenTranscriptDoesNotAutomaticallyDelegate(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Disposable Codex", Transport: "desktop"}}
	f := targetSelected(t, target)
	spoken := Event{Sequence: 27, Method: "thread/realtime/transcript/done", Params: map[string]any{"role": "user", "text": "Inspect this session"}}
	f.observe(spoken)
	f.observe(spoken)
	if len(target.sent) != 0 {
		t.Fatalf("speech was forwarded without an explicit tool instruction: %v", target.sent)
	}
}

func TestCloseWaitsForAnActionInProgressAndRefusesLaterWork(t *testing.T) {
	f := newFixture(t, fixtureOptions{})
	f.service.mu.Lock()
	closed := make(chan struct{})
	go func() { f.service.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while an action held the service")
	case <-time.After(100 * time.Millisecond):
	}
	f.service.mu.Unlock()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the action finished")
	}
	if _, err := f.Apply(Action{Action: "prepare"}); err == nil {
		t.Fatal("an action was accepted after Close")
	}
	f.service.mu.Lock()
	f.service.observe()
	running := f.service.running
	f.service.mu.Unlock()
	if running {
		t.Fatal("event polling started after Close")
	}
}

func TestDynamicVoiceToolsTransferAndReturnThroughSharedState(t *testing.T) {
	target := &targetFixture{session: surface.Session{ID: "target-a", Surface: surface.KindCodex, Name: "Build session", Transport: "desktop"}}
	f := newFixture(t, fixtureOptions{target: target, stream: newJournalFixture()})
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
	f, provider := fixture(t)
	startFixture(t, f)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	p := provider.params[0]
	for key := range p {
		if strings.Contains(strings.ToLower(key), "token") || strings.Contains(strings.ToLower(key), "apikey") {
			t.Fatal("credential in voice params")
		}
	}
	prompt, _ := p["prompt"].(string)
	if !strings.HasPrefix(prompt, realtimeSystemPrompt) || !strings.Contains(prompt, "No worker is selected") {
		t.Fatal("realtime system prompt must replace the built-in prompt without inherited startup context")
	}
	if !strings.Contains(realtimeSystemPrompt, "never authorizes an unsolicited check-in or interruption") || !strings.Contains(realtimeSystemPrompt, "never restart or repeat") {
		t.Fatal("realtime prompt must distinguish background observations from request-owned answers")
	}
}

func TestCompletedAnswersAreNotReinjectedAsSessionSpeech(t *testing.T) {
	f, p := fixture(t)
	connectFixture(t, f)
	f.observe(Event{Method: "turn/started", Params: map[string]any{"turn": map[string]any{"id": "current-turn"}}})
	f.observe(Event{Method: "item/completed", Params: map[string]any{"turnId": "current-turn", "item": map[string]any{"id": "final", "type": "agentMessage", "phase": "final_answer", "text": "The selected task replied."}}})
	if requests, _, _ := p.snapshot(); len(requests) != 1 || requests[0] != "voice/call/start" {
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
	if !reflect.DeepEqual(requests, []string{"voice/call/start"}) || interrupts != 0 {
		t.Fatalf("watchdog requests=%v interrupts=%d", requests, interrupts)
	}
	if v := f.View(); v.Phase != "ended" {
		t.Fatalf("hangup not represented: %+v", v)
	}
}

func TestSDPRequiresMatchingStartAndEventLossPreventsReattachment(t *testing.T) {
	f, _ := fixture(t)
	startFixture(t, f)
	f.observe(Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": "old-call"}}, Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0 old"}})
	if f.View().SDP != "v=0 answer" {
		t.Fatal("legacy events changed the direct call answer")
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
	if requests, _, _ := p.snapshot(); !reflect.DeepEqual(requests, []string{"voice/call/start"}) {
		t.Fatalf("audio teardown skipped: %v", requests)
	}
}
