package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestRealtimeSettingsUseNativeDelegationSpeechAndNoCredentials(t *testing.T) {
	p := StartParams("operator", "call", "offer")
	if p["codexResponseHandoffMode"] != "bemTags" || p["version"] != "v3" || p["outputModality"] != "audio" {
		t.Fatalf("params=%v", p)
	}
	if _, ok := p["clientManagedHandoffs"]; ok {
		t.Fatal("native response routing was disabled")
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
