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
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type daemonSurface struct {
	kind           surface.SurfaceKind
	sessions       map[string]surface.Session
	observations   map[string]*surface.TurnObservation
	accepted       bool
	nilResult      bool
	sent           []string
	steered        []string
	models         []string
	modelOptions   []surface.ModelOption
	listCalls      atomic.Int32
	resolveCalls   atomic.Int32
	rejectBusy     bool
	sendErr        error
	turnID         string
	observeErr     error
	observeCalls   atomic.Int32
	startOptions   []surface.SessionStartOptions
	startErr       error
	caps           surface.Capabilities
	streamEvents   []surface.StreamEvent
	streamErr      error
	contextUsage   *surface.ContextUsage
	searchResults  []surface.SessionSearchResult
	searchErr      error
	compactCalls   atomic.Int32
	compactRelease <-chan struct{}
	compactErr     error
}

type runtimeDaemonSurface struct {
	*daemonSurface
	ensureCalls atomic.Int32
	ensureErr   error
}

type accessDaemonSurface struct {
	*daemonSurface
	ensureCalls atomic.Int32
}

func (f *accessDaemonSurface) EnsureWritable(context.Context, *surface.Session) error {
	f.ensureCalls.Add(1)
	return nil
}

type refreshAwareClaudeSurface struct {
	*daemonSurface
	liveLastActive time.Time
}

type transportRefreshingSurface struct {
	*daemonSurface
}

func (f *transportRefreshingSurface) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	session.Source = "vscode"
	session.Transport = "desktop"
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
}

type failingClaudeListSurface struct {
	*daemonSurface
}

func (f *failingClaudeListSurface) List(context.Context) ([]surface.Session, error) {
	return nil, errors.New("Claude session metadata unavailable")
}

func (f *refreshAwareClaudeSurface) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	if session.Status == surface.StatusBusy && session.LastActive.Equal(f.liveLastActive) {
		return &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "live"}, nil
	}
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
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
func (f *daemonSurface) SearchSessions(context.Context, string, int) ([]surface.SessionSearchResult, error) {
	return f.searchResults, f.searchErr
}
func (f *daemonSurface) Observe(_ context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	f.observeCalls.Add(1)
	if f.observeErr != nil {
		return nil, f.observeErr
	}
	return f.observations[session.ID], nil
}

func TestScanBacksOffFailingRelayOnlySessionsAndRecovers(t *testing.T) {
	d, registry, fake, from, to := daemonFixture(t)
	to.Status = surface.StatusOffline
	if err := registry.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	fake.observeErr = errors.New("desktop bridge unavailable")
	d.scanAndRelay(context.Background())
	d.scanAndRelay(context.Background())
	if got := fake.observeCalls.Load(); got != 1 {
		t.Fatalf("observation calls=%d, want one during backoff", got)
	}
	if err := registry.QueueMessage(from.ID, "deliver after recovery"); err != nil {
		t.Fatal(err)
	}
	d.scanAndRelay(context.Background())
	if got := fake.observeCalls.Load(); got != 2 {
		t.Fatalf("observation calls=%d, queued work must bypass backoff", got)
	}
	d.retryMu.Lock()
	retry := d.observeRetry[from.ID]
	retry.retryAt = time.Now().Add(-time.Millisecond)
	d.observeRetry[from.ID] = retry
	d.retryMu.Unlock()
	fake.observeErr = nil
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.scanAndRelay(context.Background())
	if got := fake.observeCalls.Load(); got != 3 {
		t.Fatalf("observation calls=%d, want recovery attempt", got)
	}
	if _, found := d.observeRetry[from.ID]; found {
		t.Fatal("successful observation did not clear backoff")
	}
}

func TestPruneObservationFailuresClearsLocalNotificationArms(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	d.armCompletionNotification(from.ID)
	d.pruneObservationFailures(nil)
	if d.consumeNotificationArm(from.ID) {
		t.Fatal("unwatched session retained local notification arm")
	}
}
func (f *daemonSurface) Send(_ context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	if f.rejectBusy && session.Status == surface.StatusBusy {
		return &surface.SendResult{Accepted: false}, nil
	}
	f.sent = append(f.sent, message)
	if f.nilResult {
		return nil, nil
	}
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
	if f.nilResult {
		return nil, nil
	}
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
func (f *daemonSurface) Compact(ctx context.Context, _ *surface.Session) error {
	f.compactCalls.Add(1)
	if f.compactRelease != nil {
		select {
		case <-f.compactRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.compactErr
}
func (*daemonSurface) Model(context.Context, *surface.Session, string) (string, error) {
	return "", nil
}
func (f *daemonSurface) Models(context.Context) ([]surface.ModelOption, error) {
	return f.modelOptions, nil
}
func (*daemonSurface) Interrupt(context.Context, *surface.Session) error { return nil }
func (f *daemonSurface) Steer(_ context.Context, _ *surface.Session, message string) error {
	f.steered = append(f.steered, message)
	return nil
}
func (f *daemonSurface) Capabilities() surface.Capabilities { return f.caps }
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

func TestObservationPersistsRefreshedCodexTransport(t *testing.T) {
	d, r, base, _, target := daemonFixture(t)
	target.Source = "agenthail"
	target.Transport = "managed"
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	adapter := &transportRefreshingSurface{daemonSurface: base}
	d.Surfaces = []surface.Surface{adapter}
	if err := r.QueueMessage(target.ID, "deliver after Desktop refresh"); err != nil {
		t.Fatal(err)
	}
	d.scanAndRelay(context.Background())
	stored, err := r.Session(target.ID)
	if err != nil || stored.Source != "vscode" || stored.Transport != "desktop" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if r.QueueCount(target.ID) != 0 || len(base.sent) != 1 {
		t.Fatalf("pending=%d sent=%v", r.QueueCount(target.ID), base.sent)
	}
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

func TestBusySteerRelayUsesDurableQueueUntilOutbox(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: "steer"}); err != nil {
		t.Fatal(err)
	}
	d, r, fake, from, to := daemonFixture(t)
	to.Status = surface.StatusBusy
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	fake.caps = surface.Capabilities{Send: true, Steer: true}
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	d.fireRelays(&from, "relay-turn", 0, "forwarded reply")
	if len(fake.steered) != 0 {
		t.Fatalf("relay steered before outbox: %v", fake.steered)
	}
	item, err := r.QueueItem(1)
	if err != nil || item.BusyDelivery != "steer" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	d.drainSteerMessageQueue(context.Background(), fake, &to)
	if len(fake.steered) != 1 || r.QueueCount(to.ID) != 0 {
		t.Fatalf("steered=%v queued=%d", fake.steered, r.QueueCount(to.ID))
	}
}

func TestObservationKeepsQueueItemsBlockedWhileSteerIsPending(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true}
	if _, err := r.QueueMessageWithOptions(to.ID, "ordinary queued", "queue:1", surface.SendOptions{BusyDelivery: "queue"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions(to.ID, "steer queued", "steer:1", surface.SendOptions{BusyDelivery: "steer"}); err != nil {
		t.Fatal(err)
	}
	for _, observation := range []*surface.TurnObservation{
		{Status: surface.StatusUnknown},
		{Status: surface.StatusOffline},
		{Status: surface.StatusIdle, ActiveTurnID: "still-running"},
	} {
		fake.observations[to.ID] = observation
		d.observeSession(context.Background(), fake, &to)
		if len(fake.sent) != 0 || len(fake.steered) != 0 || r.QueueCount(to.ID) != 2 {
			t.Fatalf("status=%s active=%q sent=%v steered=%v queued=%d", observation.Status, observation.ActiveTurnID, fake.sent, fake.steered, r.QueueCount(to.ID))
		}
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-busy"}
	d.observeSession(context.Background(), fake, &to)
	if len(fake.sent) != 0 || len(fake.steered) != 1 || fake.steered[0] != "steer queued" || r.QueueCount(to.ID) != 1 {
		t.Fatalf("busy sent=%v steered=%v queued=%d", fake.sent, fake.steered, r.QueueCount(to.ID))
	}
}

func TestRelayOutboxPreservesOptionsWhenBusySteerIsConfigured(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	to.Status = surface.StatusBusy
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	fake.caps = surface.Capabilities{Send: true, Steer: true}
	queueID, err := r.QueueRelayMessageWithOptions(to.ID, "relay with options", "relay:options", 1, surface.SendOptions{Model: "model-a", BusyDelivery: "steer", TurnOptions: surface.TurnOptions{Effort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.QueueItem(queueID)
	if err != nil || item.BusyDelivery != "queue" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	d.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.steered) != 0 || len(fake.models) != 1 || fake.models[0] != "model-a" || r.QueueCount(to.ID) != 0 {
		t.Fatalf("steered=%v models=%v queued=%d", fake.steered, fake.models, r.QueueCount(to.ID))
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

func TestCodexCompletionReconcilesMatchingDeliveryIntent(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindCodex
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "sender", Surface: surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: from.ID, ProviderKey: "turn-next", Status: registry.DeliveryIntentSent, Evidence: surface.EvidenceDelivered})
	if err != nil {
		t.Fatal(err)
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-before", Reply: &surface.ReplyResult{Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-next", Reply: &surface.ReplyResult{Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	stored, err := r.DeliveryIntent(intent.ID)
	if err != nil || stored.Status != registry.DeliveryIntentDelivered {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
}

func TestFailedCodexCompletionDoesNotReconcileDeliveryIntent(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	if err := r.RegisterSession(surface.Session{ID: "sender", Surface: surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: from.ID, ProviderKey: "turn-failed", Status: registry.DeliveryIntentSent, Evidence: surface.EvidenceDelivered})
	if err != nil {
		t.Fatal(err)
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-before", Reply: &surface.ReplyResult{Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-failed", Reply: &surface.ReplyResult{Done: true, Error: "model error"}}
	daemon.observeSession(context.Background(), fake, &from)
	stored, err := r.DeliveryIntent(intent.ID)
	if err != nil || stored.Status != registry.DeliveryIntentSent {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
}

func TestClaudeCompletionReconcilesOnlyByInputEnvelope(t *testing.T) {
	daemon, r, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindClaude
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "sender", Surface: surface.KindClaude}); err != nil {
		t.Fatal(err)
	}
	intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: from.ID, ProviderKey: "user-envelope", Status: registry.DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted})
	if err != nil {
		t.Fatal(err)
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "assistant-baseline", InputTurnID: "other-envelope", Reply: &surface.ReplyResult{Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "assistant-completion", InputTurnID: "user-envelope", Reply: &surface.ReplyResult{Done: true}}
	daemon.observeSession(context.Background(), fake, &from)
	stored, err := r.DeliveryIntent(intent.ID)
	if err != nil || stored.Status != registry.DeliveryIntentDelivered {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
}

func TestQueuedDeliveryBindsProviderTurnForReconciliation(t *testing.T) {
	daemon, r, fake, from, to := daemonFixture(t)
	_, deliveryID, err := r.QueueDeliveryWithIntent(to.ID, "deliver later", "", surface.SendOptions{SourceSessionID: from.ID})
	if err != nil || deliveryID == 0 {
		t.Fatalf("deliveryID=%d err=%v", deliveryID, err)
	}
	fake.turnID = "turn-queued"
	daemon.drainMessageQueue(context.Background(), fake, &to)
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != registry.DeliveryIntentSent || stored.ProviderKey != "turn-queued" {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-before", Reply: &surface.ReplyResult{Done: true}}
	daemon.observeSession(context.Background(), fake, &to)
	if err := r.MarkDeliveryStarted(to.ID, "turn-queued", "turn-before"); err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-queued", Reply: &surface.ReplyResult{Done: true, Text: "done"}}
	daemon.observeSession(context.Background(), fake, &to)
	stored, err = r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != registry.DeliveryIntentDelivered {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
}

func TestQueuedTerminalFailureNotifiesSenderOnce(t *testing.T) {
	daemon, r, fake, from, to := daemonFixture(t)
	_, deliveryID, err := r.QueueDeliveryWithIntent(to.ID, "deliver later", "", surface.SendOptions{SourceSessionID: from.ID})
	if err != nil {
		t.Fatal(err)
	}
	fake.sendErr = surface.DeliveryTerminal(errors.New("target rejected input"), surface.DeliveryInvalidRequest)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != registry.DeliveryIntentFailed || stored.NotificationQueueID == 0 {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
	if count := r.QueueCount(from.ID); count != 1 {
		t.Fatalf("sender notices=%d", count)
	}
}

func TestQueuedRelayTerminalFailureNotifiesSourceOnce(t *testing.T) {
	daemon, r, fake, from, to := daemonFixture(t)
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	daemon.fireRelays(&from, "relay-turn", 0, "forwarded reply")
	fake.sendErr = surface.DeliveryTerminal(errors.New("target rejected input"), surface.DeliveryInvalidRequest)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	daemon.fireRelays(&from, "relay-turn", 0, "forwarded reply")
	if count := r.QueueCount(from.ID); count != 1 {
		t.Fatalf("source notices=%d", count)
	}
	window, err := r.CatalogEventsAfter(0, 10)
	if events := withoutQueueCatalogEvents(window.Events); err != nil || len(events) != 1 || events[0].Type != "delivery.problem" {
		t.Fatalf("events=%+v err=%v", window, err)
	}
}

func TestQueuedRelaySuccessDoesNotCreateDeliveryProblem(t *testing.T) {
	daemon, r, fake, from, to := daemonFixture(t)
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	daemon.fireRelays(&from, "relay-turn", 0, "forwarded reply")
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if count := r.QueueCount(from.ID); count != 0 {
		t.Fatalf("source notices=%d", count)
	}
	window, err := r.CatalogEventsAfter(0, 10)
	if err != nil || len(withoutQueueCatalogEvents(window.Events)) != 0 {
		t.Fatalf("events=%+v err=%v", window, err)
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
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(context.Background(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	d.observeSession(context.Background(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-2", CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Done: true}}
	d.observeSession(context.Background(), fake, &from)
	select {
	case <-received:
		t.Fatal("active follow-up turn emitted a completion notification")
	case <-time.After(250 * time.Millisecond):
	}
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
		if payload["turnId"] != "turn-2" {
			t.Fatalf("turnId=%q", payload["turnId"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mobile completion notification was not sent")
	}
}

func TestCompletionNotificationDoesNotSendForBusyBaseline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
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
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		received <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "old-turn"}
	d.observeSession(context.Background(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "old-completion", Reply: &surface.ReplyResult{Done: true}}
	d.observeSession(context.Background(), fake, &from)
	select {
	case <-received:
		t.Fatal("busy baseline emitted a completion notification")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestCompletionNotificationRequiresFreshRuntimeState(t *testing.T) {
	for _, test := range []struct {
		name           string
		state          registry.RuntimeState
		observedActive bool
		want           bool
	}{
		{name: "unarmed baseline", state: registry.RuntimeState{}, want: false},
		{name: "observed active turn", state: registry.RuntimeState{}, observedActive: true, want: true},
		{name: "agenthail delivery", state: registry.RuntimeState{NotificationArmed: true}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := completionNotificationEligible(test.state, test.observedActive); got != test.want {
				t.Fatalf("completionNotificationEligible=%v want=%v", got, test.want)
			}
		})
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
	_, _ = d.refreshAndPruneInactiveClaudeRoutes(context.Background(), time.Now().Add(time.Hour), true)
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
	_, _ = d.refreshAndPruneInactiveClaudeRoutes(context.Background(), time.Now().Add(time.Hour), true)
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

func TestOneShotRelayQueuesOnlyFirstMatchingCompletion(t *testing.T) {
	daemon, r, _, from, to := daemonFixture(t)
	if _, err := r.AddRouteWithOptions(from.ID, to.ID, ".*", true); err != nil {
		t.Fatal(err)
	}
	daemon.fireRelays(&from, "completion-one", 0, "first")
	daemon.fireRelays(&from, "completion-two", 0, "second")
	if got := r.QueueCount(to.ID); got != 1 {
		t.Fatalf("queued=%d, want one", got)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].Active || routes[0].FireCount != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
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

func TestScanSkipsClaudeDiscoveryWithoutClaudeWork(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	claude := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"open": {ID: "open", Surface: surface.KindClaude, Status: surface.StatusBusy}}}
	d.Surfaces = []surface.Surface{claude}

	d.scanAndRelay(context.Background())

	if got := claude.listCalls.Load(); got != 0 {
		t.Fatalf("Claude discovery calls=%d", got)
	}
}

func TestScanDoesNotStartSurfaceRuntime(t *testing.T) {
	daemon, _, fake, _, _ := daemonFixture(t)
	runtimeSurface := &runtimeDaemonSurface{daemonSurface: fake}
	daemon.Surfaces = []surface.Surface{runtimeSurface}
	daemon.scanAndRelay(context.Background())
	if runtimeSurface.ensureCalls.Load() != 0 {
		t.Fatalf("ensure calls=%d", runtimeSurface.ensureCalls.Load())
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

func TestScanDoesNotDrainQueueWithUnknownStatus(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	to.Status = surface.StatusBusy
	if err := r.RegisterSession(to); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(to.ID, "wait for a known idle state"); err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusUnknown}

	daemon.scanAndRelay(context.Background())

	if r.QueueCount(to.ID) != 1 || len(fake.sent) != 0 {
		t.Fatalf("pending=%d sent=%v", r.QueueCount(to.ID), fake.sent)
	}
}

func TestScanRefreshesClaudeMetadataWithoutRoutesBeforeQueueDrain(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	oldLastActive := time.Now().Add(-time.Hour)
	liveLastActive := time.Now().Truncate(time.Millisecond)
	stale := surface.Session{ID: "claude-queued", Surface: surface.KindClaude, Status: surface.StatusIdle, LastActive: oldLastActive}
	live := stale
	live.Status = surface.StatusBusy
	live.LastActive = liveLastActive
	if err := r.RegisterSession(stale); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(stale.ID, "wait for completion"); err != nil {
		t.Fatal(err)
	}
	base := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{live.ID: live}, accepted: true}
	claude := &refreshAwareClaudeSurface{daemonSurface: base, liveLastActive: liveLastActive}
	d.Surfaces = []surface.Surface{claude}

	d.scanAndRelay(context.Background())

	registered, err := r.Session(stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if registered.Status != surface.StatusBusy || !registered.LastActive.Equal(liveLastActive) {
		t.Fatalf("registered=%+v", registered)
	}
	if r.QueueCount(stale.ID) != 1 || len(base.sent) != 0 {
		t.Fatalf("pending=%d sent=%v", r.QueueCount(stale.ID), base.sent)
	}
}

func TestScanDoesNotDrainClaudeQueueWhenMetadataRefreshFails(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	session := surface.Session{ID: "claude-stale", Surface: surface.KindClaude, Status: surface.StatusIdle, LastActive: time.Now().Add(-time.Hour)}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(session.ID, "wait for a safe refresh"); err != nil {
		t.Fatal(err)
	}
	base := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{session.ID: session}, observations: map[string]*surface.TurnObservation{session.ID: {Status: surface.StatusIdle}}, accepted: true}
	d.Surfaces = []surface.Surface{&failingClaudeListSurface{daemonSurface: base}}

	d.scanAndRelay(context.Background())

	if r.QueueCount(session.ID) != 1 || len(base.sent) != 0 {
		t.Fatalf("pending=%d sent=%v", r.QueueCount(session.ID), base.sent)
	}
}

func TestScanLoadsUnloadedDesktopCodexSessionForQueueDelivery(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	session := surface.Session{ID: "desktop-unloaded", Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "desktop"}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(session.ID, "deliver after Desktop load"); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{session.ID: session}, observations: map[string]*surface.TurnObservation{session.ID: {Status: surface.SessionStatus("notLoaded")}}, accepted: true}
	d.Surfaces = []surface.Surface{fake}

	d.scanAndRelay(context.Background())

	if len(fake.sent) != 1 || fake.sent[0] != "deliver after Desktop load" || r.QueueCount(session.ID) != 0 {
		t.Fatalf("sent=%v pending=%d", fake.sent, r.QueueCount(session.ID))
	}
}

func TestQueuedCompactBlocksFollowingMessageUntilCompletion(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	release := make(chan struct{})
	fake.compactRelease = release
	if _, err := r.QueueCompact(to.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(to.ID, "follow-up"); err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}

	daemon.scanAndRelay(context.Background())
	deadline := time.Now().Add(time.Second)
	for fake.compactCalls.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(fake.sent) != 0 || r.QueueCount(to.ID) != 2 || fake.compactCalls.Load() != 1 {
		t.Fatalf("while compacting sent=%v pending=%d compact=%d", fake.sent, r.QueueCount(to.ID), fake.compactCalls.Load())
	}

	close(release)
	deadline = time.Now().Add(time.Second)
	for r.QueueCount(to.ID) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	daemon.scanAndRelay(context.Background())
	if len(fake.sent) != 1 || fake.sent[0] != "follow-up" || r.QueueCount(to.ID) != 0 {
		t.Fatalf("after completion sent=%v pending=%d", fake.sent, r.QueueCount(to.ID))
	}
}

func TestQueuedCompactUnknownOutcomeIsNotRetried(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	fake.compactErr = surface.DeliveryOutcomeUnknown(errors.New("confirmation lost"))
	id, err := r.QueueCompact(to.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	daemon.scanAndRelay(context.Background())
	deadline := time.Now().Add(time.Second)
	var item *registry.QueueRow
	for time.Now().Before(deadline) {
		item, err = r.QueueItem(id)
		if err == nil && item.Status == "dead" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || item == nil || item.Status != "dead" || item.Evidence != surface.EvidenceUnknown {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	daemon.scanAndRelay(context.Background())
	if fake.compactCalls.Load() != 1 {
		t.Fatalf("compact calls=%d", fake.compactCalls.Load())
	}
}

func TestQueuedClaudeCompactIsDeliveredOnlyAfterTranscriptBoundary(t *testing.T) {
	daemon, r, _, _, _ := daemonFixture(t)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","message":{"content":"ready"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: "session_compact", Surface: surface.KindClaude, Status: surface.StatusIdle, Transcript: path}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	id, err := r.QueueCompact(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	requestSent := make(chan string, 1)
	claude := surfaces.NewClaudeWithRequest("", t.TempDir(), func(_ context.Context, method, url string, _ map[string]string, body, _ string, _ string, _ time.Duration) (int, string, error) {
		if method != "POST" || !strings.Contains(url, "/v1/code/sessions/") {
			t.Fatalf("method=%s url=%s", method, url)
		}
		requestSent <- body
		return 200, `{}`, nil
	})

	daemon.drainMessageQueue(context.Background(), claude, &session)
	var requestUUID string
	select {
	case body := <-requestSent:
		if !strings.Contains(body, `"content":"/compact"`) {
			t.Fatalf("body=%s", body)
		}
		var envelope struct {
			Events []struct {
				Payload struct {
					UUID string `json:"uuid"`
				} `json:"payload"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope.Events) != 1 {
			t.Fatalf("body=%s err=%v", body, err)
		}
		requestUUID = envelope.Events[0].Payload.UUID
	case <-time.After(time.Second):
		t.Fatal("compact command was not sent")
	}
	item, err := r.QueueItem(id)
	if err != nil || item.Status != "inflight" || item.Evidence == surface.EvidenceDelivered {
		t.Fatalf("before boundary item=%+v err=%v", item, err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(f, "{\"type\":\"user\",\"uuid\":%q,\"message\":{\"content\":\"/compact\"}}\n{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"content\":\"done\"}\n", requestUUID); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		item, err = r.QueueItem(id)
		if err == nil && item.Status == "delivered" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || item.Status != "delivered" || item.Evidence != surface.EvidenceDelivered {
		t.Fatalf("after boundary item=%+v err=%v", item, err)
	}
	daemon.queueWorkers.Wait()
}

func TestQueueWaitsForBridgeRecoveryAndDeliversExactlyOnce(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage(to.ID, "deliver after bridge recovery"); err != nil {
		t.Fatal(err)
	}
	fake.observeErr = errors.New("Codex Desktop request dispatcher is unavailable")
	daemon.scanAndRelay(context.Background())
	daemon.scanAndRelay(context.Background())
	fake.observeErr = errors.New("Codex Desktop bridge was replaced; rebinding")
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

func TestReplyForwardFailureNotifiesActualSenderOnce(t *testing.T) {
	daemon, r, _, from, to := daemonFixture(t)
	if err := r.RegisterSession(surface.Session{ID: to.ID, Surface: surface.KindNotion, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	daemon.fireRelays(&from, "reply-turn", 0, "reply body")
	daemon.fireRelays(&from, "reply-turn", 0, "reply body")
	if count := r.QueueCount(from.ID); count != 1 {
		t.Fatalf("sender notices=%d", count)
	}
	window, err := r.CatalogEventsAfter(0, 10)
	problems := 0
	for _, event := range window.Events {
		if event.Type == "delivery.problem" {
			problems++
		}
	}
	if err != nil || problems != 1 {
		t.Fatalf("events=%+v err=%v", window, err)
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

func TestOutboxDeadLettersEmptyResultWithoutAutomaticRetry(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "empty result"); err != nil {
		t.Fatal(err)
	}
	fake.nilResult = true
	daemon.drainMessageQueue(context.Background(), fake, &to)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.sent) != 1 {
		t.Fatalf("empty result retried %d times", len(fake.sent))
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || !strings.Contains(rows[0].LastError, "outcome is unknown") {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestOutboxDefersPreDeliveryFailureWithoutDeadLettering(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "wait for bridge"); err != nil {
		t.Fatal(err)
	}
	fake.sendErr = surface.DeliveryUnavailable(context.DeadlineExceeded)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	item, err := r.QueueItem(1)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "pending" || item.Attempts != 1 || !strings.Contains(item.LastError, "delivery did not start") {
		t.Fatalf("item=%+v", item)
	}
}

func TestOutboxDeadLettersTerminalDeliveryWithoutRetry(t *testing.T) {
	daemon, r, fake, _, to := daemonFixture(t)
	if err := r.QueueMessage("to", "stale session"); err != nil {
		t.Fatal(err)
	}
	fake.sendErr = surface.DeliveryTerminal(errors.New("HTTP 404: session not found"))
	daemon.drainMessageQueue(context.Background(), fake, &to)
	daemon.drainMessageQueue(context.Background(), fake, &to)
	if len(fake.sent) != 1 {
		t.Fatalf("terminal delivery retried %d times", len(fake.sent))
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || !strings.Contains(rows[0].LastError, "delivery rejected") {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}
