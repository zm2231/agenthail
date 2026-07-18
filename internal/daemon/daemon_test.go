package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type daemonSurface struct {
	kind         surface.SurfaceKind
	sessions     map[string]surface.Session
	observations map[string]*surface.TurnObservation
	accepted     bool
	sent         []string
	models       []string
	modelOptions []surface.ModelOption
	listCalls    atomic.Int32
	resolveCalls atomic.Int32
	rejectBusy   bool
	sendErr      error
	turnID       string
	observeErr   error
	startOptions []surface.SessionStartOptions
	startErr     error
	caps         surface.Capabilities
	streamEvents []surface.StreamEvent
	streamErr    error
	contextUsage *surface.ContextUsage
}

type runtimeDaemonSurface struct {
	*daemonSurface
	ensureCalls atomic.Int32
	ensureErr   error
}

type healthDaemonSurface struct {
	*daemonSurface
	healthErr error
	runtime   surface.RuntimeStatus
}

func (f *healthDaemonSurface) Health(context.Context) error {
	return f.healthErr
}

func (f *healthDaemonSurface) RuntimeStatus(context.Context) surface.RuntimeStatus {
	return f.runtime
}

func (f *runtimeDaemonSurface) EnsureRuntime(context.Context) error {
	f.ensureCalls.Add(1)
	return f.ensureErr
}

func (f *daemonSurface) Name() surface.SurfaceKind {
	if f.kind != "" {
		return f.kind
	}
	return surface.KindCodex
}
func (f *daemonSurface) List(context.Context) ([]surface.Session, error) {
	f.listCalls.Add(1)
	result := make([]surface.Session, 0, len(f.sessions))
	for _, session := range f.sessions {
		result = append(result, session)
	}
	return result, nil
}
func (f *daemonSurface) Resolve(_ context.Context, id string) (*surface.Session, error) {
	f.resolveCalls.Add(1)
	session, ok := f.sessions[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &session, nil
}
func (f *daemonSurface) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	if f.observeErr != nil {
		return nil, f.observeErr
	}
	return f.observations[session.ID], nil
}
func (f *daemonSurface) Send(_ context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	if f.rejectBusy && session.Status == surface.StatusBusy {
		return &surface.SendResult{Accepted: false}, nil
	}
	f.sent = append(f.sent, message)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	turnID := f.turnID
	if turnID == "" {
		turnID = "turn"
	}
	return &surface.SendResult{UUID: turnID, Accepted: f.accepted}, nil
}
func (f *daemonSurface) SendWithOptions(_ context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	if f.rejectBusy && session.Status == surface.StatusBusy {
		return &surface.SendResult{Accepted: false}, nil
	}
	f.sent = append(f.sent, message)
	f.models = append(f.models, options.Model)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	turnID := f.turnID
	if turnID == "" {
		turnID = "turn"
	}
	return &surface.SendResult{UUID: turnID, Accepted: f.accepted}, nil
}
func (f *daemonSurface) StartSession(_ context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	f.startOptions = append(f.startOptions, options)
	session := &surface.Session{ID: "started", Surface: surface.KindCodex, Name: "Started conversation", Cwd: options.Cwd, Status: surface.StatusBusy, Source: "appServer", Transport: "managed", LastActive: time.Now()}
	f.sessions[session.ID] = *session
	if f.startErr != nil {
		return session, nil, f.startErr
	}
	return session, &surface.SendResult{UUID: "started-turn", Accepted: true}, nil
}
func (*daemonSurface) Reply(context.Context, *surface.Session, int) (*surface.ReplyResult, error) {
	return nil, nil
}
func (*daemonSurface) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return nil, nil
}
func (f *daemonSurface) Stream(_ context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	for _, event := range f.streamEvents {
		onEvent(event)
	}
	return f.streamErr
}
func (*daemonSurface) GoalSet(context.Context, *surface.Session, string) error { return nil }
func (*daemonSurface) GoalClear(context.Context, *surface.Session) error       { return nil }
func (*daemonSurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}
func (*daemonSurface) Compact(context.Context, *surface.Session) error { return nil }
func (*daemonSurface) Model(context.Context, *surface.Session, string) (string, error) {
	return "", nil
}
func (f *daemonSurface) Models(context.Context) ([]surface.ModelOption, error) {
	return f.modelOptions, nil
}
func (*daemonSurface) Interrupt(context.Context, *surface.Session) error     { return nil }
func (*daemonSurface) Steer(context.Context, *surface.Session, string) error { return nil }
func (f *daemonSurface) Capabilities() surface.Capabilities                  { return f.caps }
func (f *daemonSurface) ContextUsage(context.Context, *surface.Session) (*surface.ContextUsage, error) {
	return f.contextUsage, nil
}

func daemonFixture(t *testing.T) (*Daemon, *registry.Registry, *daemonSurface, surface.Session, surface.Session) {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	from := surface.Session{ID: "from", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	to := surface.Session{ID: "to", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{sessions: map[string]surface.Session{"from": from, "to": to}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	return New(r, []surface.Surface{fake}), r, fake, from, to
}

func TestObservationBaselinesThenRelaysOnceAndQueuesBusyTarget(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Text: "one", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	if len(fake.sent) != 0 {
		t.Fatalf("baseline relayed: %v", fake.sent)
	}

	fake.accepted = false
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-2", Reply: &surface.ReplyResult{Text: "same", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	if len(fake.sent) != 0 || r.QueueCount("to") != 1 {
		t.Fatalf("sent=%v pending=%d", fake.sent, r.QueueCount("to"))
	}
	daemon.observeSession(context.Background(), fake, &from)
	if len(fake.sent) != 0 || r.QueueCount("to") != 1 {
		t.Fatalf("duplicate: sent=%v pending=%d", fake.sent, r.QueueCount("to"))
	}
}

func TestObservationPublishesOnlyWhenRuntimeStateChanges(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}
	d.observeSession(context.Background(), fake, &from)
	firstCount := len(d.events.history)
	if firstCount == 0 {
		t.Fatal("baseline observation did not publish")
	}
	d.observeSession(context.Background(), fake, &from)
	if len(d.events.history) != firstCount {
		t.Fatalf("unchanged observation published %d additional event(s)", len(d.events.history)-firstCount)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-2", CompletedTurnID: "turn-1"}
	d.observeSession(context.Background(), fake, &from)
	if len(d.events.history) != firstCount+1 {
		t.Fatalf("changed observation events=%d want=%d", len(d.events.history), firstCount+1)
	}
}

func TestAcceptedDeliveryMakesFirstCompletionObservable(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDeliveryStarted(from.ID, "request-1", ""); err != nil {
		t.Fatal(err)
	}
	fake.accepted = false
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "response-1", Reply: &surface.ReplyResult{Text: "done", Done: true}}
	d.observeSession(context.Background(), fake, &from)
	if count := r.QueueCount("to"); count != 1 {
		t.Fatalf("first completion was only baselined: queued=%d", count)
	}
}

func TestAcceptedDeliveryDoesNotRelayPreviousCompletion(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkDeliveryStarted(from.ID, "request-1", ""); err != nil {
		t.Fatal(err)
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "request-1", CompletedTurnID: "previous", Reply: &surface.ReplyResult{Text: "old", Done: true}}
	d.observeSession(context.Background(), fake, &from)
	if count := r.QueueCount("to"); count != 0 {
		t.Fatalf("preexisting completion relayed: queued=%d", count)
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "response-1", Reply: &surface.ReplyResult{Text: "new", Done: true}}
	d.observeSession(context.Background(), fake, &from)
	if count := r.QueueCount("to"); count != 1 {
		t.Fatalf("new completion not relayed: queued=%d", count)
	}
}

func TestMobileCompletionNotificationDoesNotExposeSessionDisplay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
	if err := r.SetAlias("private-project-title", from.ID); err != nil {
		t.Fatal(err)
	}
	pairing, err := r.CreateDevicePairing("Phone", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := r.CompleteDevicePairing(pairing.Secret, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		received <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Done: true}}
	d.observeSession(context.Background(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-2", Reply: &surface.ReplyResult{Done: true}}
	d.observeSession(context.Background(), fake, &from)
	select {
	case payload := <-received:
		if payload["message"] != "Codex agent finished" || strings.Contains(payload["message"], "private-project-title") {
			t.Fatalf("payload=%+v", payload)
		}
		if payload["sessionId"] != from.ID {
			t.Fatalf("sessionId=%q want=%q", payload["sessionId"], from.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mobile completion notification was not sent")
	}
}

func TestRelayBoundsVerboseCompletionText(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x", maxRelayText+1000)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline", Reply: &surface.ReplyResult{Text: "old", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	fake.accepted = false
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "verbose", Reply: &surface.ReplyResult{Text: text, Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	items, err := r.ListQueue(false)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if len(items[0].Message) > maxRelayText+200 || !strings.Contains(items[0].Message, "relay text truncated") {
		t.Fatalf("relay payload length=%d", len(items[0].Message))
	}
}

func TestRelayDoesNotQueueForClosedClaudeSession(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	closed := surface.Session{ID: "closed", Surface: surface.KindClaude, Status: surface.StatusIdle, PID: 2147483647}
	if err := r.RegisterSession(closed); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, closed.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	d.fireRelays(&from, "done", 0, "finished")
	if count := r.QueueCount(closed.ID); count != 0 {
		t.Fatalf("closed target queued=%d", count)
	}
	history, err := r.ListHistory(10, closed.ID)
	if err != nil || len(history) == 0 || history[0].Kind != "relay-dropped" || !strings.Contains(history[0].Error, "no longer active") {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestInactiveClaudeRouteRebindsBeforeCleanup(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	old := surface.Session{ID: "old", Surface: surface.KindClaude, Status: surface.StatusIdle, PID: 2147483647, Transcript: "/tmp/shared.jsonl"}
	if err := r.RegisterSession(old); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, old.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	current := surface.Session{ID: "current", Surface: surface.KindClaude, Status: surface.StatusIdle, PID: os.Getpid(), Transcript: old.Transcript}
	claude := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{current.ID: current}}
	d.Surfaces = append(d.Surfaces, claude)
	d.refreshAndPruneInactiveClaudeRoutes(context.Background(), time.Now().Add(time.Hour))
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].ToSession != current.ID {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}

func TestInactiveClaudeRouteIsRemovedAfterGracePeriod(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	closed := surface.Session{ID: "closed", Surface: surface.KindClaude, Status: surface.StatusIdle, PID: 2147483647, Transcript: "/tmp/closed.jsonl"}
	offlineCodex := surface.Session{ID: "offline-codex", Surface: surface.KindCodex, Status: surface.StatusOffline}
	offlineNotion := surface.Session{ID: "offline-notion", Surface: surface.KindNotion, Status: surface.StatusOffline}
	if err := r.RegisterSession(closed); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(offlineCodex); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(offlineNotion); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, closed.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, offlineCodex.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, offlineNotion.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = append(d.Surfaces, &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}})
	d.refreshAndPruneInactiveClaudeRoutes(context.Background(), time.Now().Add(time.Hour))
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 2 || routes[0].ToSession == closed.ID || routes[1].ToSession == closed.ID {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}

func TestRelayStopsAtHopLimit(t *testing.T) {
	daemon, r, fake, _, _ := daemonFixture(t)
	for index := 0; index <= maxRelayHops; index++ {
		id := fmt.Sprintf("hop-%d", index)
		session := surface.Session{ID: id, Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		fake.sessions[id] = session
	}
	for index := 0; index < maxRelayHops; index++ {
		if _, err := r.AddRoute(fmt.Sprintf("hop-%d", index), fmt.Sprintf("hop-%d", index+1), ".*"); err != nil {
			t.Fatal(err)
		}
	}
	fake.observations["hop-0"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline-0", Reply: &surface.ReplyResult{Text: "baseline", Done: true}}
	daemon.observeSession(context.Background(), fake, sessionPointer(fake.sessions["hop-0"]))
	fake.observations["hop-0"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "done-0", Reply: &surface.ReplyResult{Text: "Done", Done: true}}
	daemon.observeSession(context.Background(), fake, sessionPointer(fake.sessions["hop-0"]))
	for index := 1; index <= maxRelayHops; index++ {
		id := fmt.Sprintf("hop-%d", index)
		fake.observations[id] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline-" + id, Reply: &surface.ReplyResult{Text: "baseline", Done: true}}
		daemon.observeSession(context.Background(), fake, sessionPointer(fake.sessions[id]))
		fake.observations[id] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "done-" + id, Reply: &surface.ReplyResult{Text: "Done", Done: true}}
		daemon.observeSession(context.Background(), fake, sessionPointer(fake.sessions[id]))
	}
	if r.QueueCount(fmt.Sprintf("hop-%d", maxRelayHops)) != 0 {
		t.Fatal("relay exceeded hop limit")
	}
	history, err := r.ListHistory(20, fmt.Sprintf("hop-%d", maxRelayHops))
	if err != nil || len(history) == 0 || history[0].Kind != "relay-dropped" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func sessionPointer(session surface.Session) *surface.Session { return &session }

func TestRelayDropsReadOnlyCodexTerminalDestination(t *testing.T) {
	daemon, r, _, from, to := daemonFixture(t)
	to.Source = "cli"
	to.Transport = "readOnly"
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	daemon.fireRelays(&from, "completion", 0, "handoff")
	if r.QueueCount(to.ID) != 0 {
		t.Fatal("read-only relay destination was queued")
	}
	history, err := r.ListHistory(10, to.ID)
	if err != nil || len(history) == 0 || history[0].Kind != "relay-dropped" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestScanObservesOnlyWatchedSessionsWithoutDiscovery(t *testing.T) {
	daemon, r, fake, _, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "from-turn"}
	fake.observations["to"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "to-turn"}

	daemon.scanAndRelay(context.Background())

	if got := fake.listCalls.Load(); got != 0 {
		t.Fatalf("daemon performed %d full surface discovery call(s)", got)
	}
	if got := fake.resolveCalls.Load(); got != 0 {
		t.Fatalf("daemon performed %d redundant resolve call(s)", got)
	}
}

func TestScanEnsuresSurfaceRuntimeBeforeObservation(t *testing.T) {
	daemon, _, fake, _, _ := daemonFixture(t)
	runtimeSurface := &runtimeDaemonSurface{daemonSurface: fake}
	daemon.Surfaces = []surface.Surface{runtimeSurface}
	daemon.scanAndRelay(context.Background())
	if runtimeSurface.ensureCalls.Load() != 1 {
		t.Fatalf("ensure calls=%d", runtimeSurface.ensureCalls.Load())
	}
}

func TestScanThrottlesRepeatedRuntimeFailuresAndRecovers(t *testing.T) {
	daemon, _, fake, _, _ := daemonFixture(t)
	runtimeSurface := &runtimeDaemonSurface{daemonSurface: fake, ensureErr: errors.New("managed runtime unavailable")}
	daemon.Surfaces = []surface.Surface{runtimeSurface}
	var output bytes.Buffer
	daemon.log = log.New(&output, "", 0)
	daemon.scanAndRelay(context.Background())
	daemon.scanAndRelay(context.Background())
	if got := strings.Count(output.String(), "managed runtime unavailable"); got != 1 {
		t.Fatalf("runtime errors=%d output=%q", got, output.String())
	}
	runtimeSurface.ensureErr = nil
	daemon.scanAndRelay(context.Background())
	runtimeSurface.ensureErr = errors.New("managed runtime unavailable")
	daemon.scanAndRelay(context.Background())
	if got := strings.Count(output.String(), "managed runtime unavailable"); got != 2 {
		t.Fatalf("runtime errors after recovery=%d output=%q", got, output.String())
	}
}

func TestScanDrainsClaudeQueueWithObservedIdleStatus(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	to.Status = surface.StatusBusy
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(to.ID, "deliver after idle"); err != nil {
		t.Fatal(err)
	}
	fake.rejectBusy = true
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "done"}

	daemon.scanAndRelay(context.Background())

	if r.QueueCount(to.ID) != 0 || len(fake.sent) != 1 || fake.sent[0] != "deliver after idle" {
		t.Fatalf("pending=%d sent=%v", r.QueueCount(to.ID), fake.sent)
	}
}

func TestQueuedCompactBlocksFollowingMessageUntilCompletion(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage(to.ID, "/compact"); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(to.ID, "follow-up"); err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}

	daemon.scanAndRelay(context.Background())
	if len(fake.sent) != 1 || fake.sent[0] != "/compact" || r.QueueCount(to.ID) != 1 {
		t.Fatalf("after compact sent=%v pending=%d", fake.sent, r.QueueCount(to.ID))
	}

	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "compact"}
	daemon.scanAndRelay(context.Background())
	if len(fake.sent) != 1 || r.QueueCount(to.ID) != 1 {
		t.Fatalf("before completion sent=%v pending=%d", fake.sent, r.QueueCount(to.ID))
	}

	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	daemon.scanAndRelay(context.Background())
	if len(fake.sent) != 2 || fake.sent[1] != "follow-up" || r.QueueCount(to.ID) != 0 {
		t.Fatalf("after completion sent=%v pending=%d", fake.sent, r.QueueCount(to.ID))
	}
}

func TestQueueWaitsForBridgeRecoveryAndDeliversExactlyOnce(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage(to.ID, "deliver after bridge recovery"); err != nil {
		t.Fatal(err)
	}
	fake.observeErr = errors.New("Codex Desktop request dispatcher is unavailable")
	daemon.scanAndRelay(context.Background())
	daemon.scanAndRelay(context.Background())
	fake.observeErr = errors.New("Codex Desktop renderer was replaced; rebinding")
	daemon.scanAndRelay(context.Background())
	item, err := r.QueueItem(1)
	if err != nil || item.Status != "pending" || item.Attempts != 0 || len(fake.sent) != 0 {
		t.Fatalf("unavailable item=%+v sent=%v err=%v", item, fake.sent, err)
	}
	fake.observeErr = nil
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	daemon.scanAndRelay(context.Background())
	daemon.scanAndRelay(context.Background())
	item, err = r.QueueItem(1)
	if err != nil || item.Status != "delivered" || item.Attempts != 1 || len(fake.sent) != 1 {
		t.Fatalf("recovered item=%+v sent=%v err=%v", item, fake.sent, err)
	}
}

func TestObservationErrorsAreThrottledUntilRecovery(t *testing.T) {
	daemon, _, fake, _, to := daemonFixture(t)
	var output bytes.Buffer
	daemon.log = log.New(&output, "", 0)
	fake.observeErr = errors.New("bridge unavailable")
	daemon.observeSession(context.Background(), fake, &to)
	daemon.observeSession(context.Background(), fake, &to)
	if strings.Count(output.String(), "bridge unavailable") != 1 {
		t.Fatalf("repeated error log=%q", output.String())
	}
	fake.observeErr = nil
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	daemon.observeSession(context.Background(), fake, &to)
	fake.observeErr = errors.New("bridge unavailable")
	daemon.observeSession(context.Background(), fake, &to)
	if strings.Count(output.String(), "bridge unavailable") != 2 {
		t.Fatalf("recovery did not reset throttle log=%q", output.String())
	}
}

func TestFailedCompletionIsNotRelayed(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline", Reply: &surface.ReplyResult{Text: "ok", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "failed", Reply: &surface.ReplyResult{Text: "partial", Done: true, Error: "turn failed"}}
	daemon.observeSession(context.Background(), fake, &from)
	if r.QueueCount("to") != 0 || len(fake.sent) != 0 {
		t.Fatalf("failed completion relayed: pending=%d sent=%v", r.QueueCount("to"), fake.sent)
	}
}

func TestRelayIsPersistedWhileDestinationIsUnavailable(t *testing.T) {
	daemon, r, fake, from, to := daemonFixture(t)
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "baseline", Reply: &surface.ReplyResult{Text: "old", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	delete(fake.sessions, "to")
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "next", Reply: &surface.ReplyResult{Text: "handoff", Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	if r.QueueCount("to") != 1 {
		t.Fatalf("relay was not durably queued: pending=%d", r.QueueCount("to"))
	}
	fake.sessions["to"] = to
	fake.accepted = true
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if r.QueueCount("to") != 0 || len(fake.sent) != 1 {
		t.Fatalf("pending=%d sent=%v", r.QueueCount("to"), fake.sent)
	}
}

func TestOutboxOnlyAcknowledgesAcceptedDelivery(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "wait"); err != nil {
		t.Fatal(err)
	}
	fake.accepted = false
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if r.QueueCount("to") != 1 {
		t.Fatal("rejected message disappeared")
	}

	daemon2, r2, fake2, _, to2 := daemonFixture(t)
	if err := r2.QueueMessage("to", "deliver"); err != nil {
		t.Fatal(err)
	}
	fake2.accepted = true
	daemon2.drainMessageQueue(context.Background(), fake2, &to2)
	if r2.QueueCount("to") != 0 || len(fake2.sent) != 1 {
		t.Fatalf("pending=%d sent=%v", r2.QueueCount("to"), fake2.sent)
	}
}

func TestOutboxPreservesQueuedModelSelection(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if _, err := r.QueueMessageWithOptions("to", "modelled", "", surface.SendOptions{Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.models) != 1 || fake.models[0] != "sonnet" || r.QueueCount("to") != 0 {
		t.Fatalf("models=%v pending=%d", fake.models, r.QueueCount("to"))
	}
}

func TestOutboxDeadLettersUnknownDeliveryWithoutAutomaticRetry(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "maybe delivered"); err != nil {
		t.Fatal(err)
	}
	fake.sendErr = surface.DeliveryOutcomeUnknown(context.DeadlineExceeded)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.sent) != 1 {
		t.Fatalf("ambiguous delivery retried %d times", len(fake.sent))
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || !strings.Contains(rows[0].LastError, "outcome is unknown") {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}
