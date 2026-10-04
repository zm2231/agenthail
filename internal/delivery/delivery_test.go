package delivery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type fakeSurface struct {
	kind         surface.SurfaceKind
	result       *surface.SendResult
	err          error
	observe      *surface.TurnObservation
	observeErr   error
	sent         []string
	steered      []string
	steerErr     error
	beforeSend   func()
	beforeSteer  func()
	capabilities surface.Capabilities
	compactCalls int
}

func (f *fakeSurface) Name() surface.SurfaceKind {
	if f.kind != "" {
		return f.kind
	}
	return surface.KindCodex
}
func (*fakeSurface) List(context.Context) ([]surface.Session, error)           { return nil, nil }
func (*fakeSurface) Resolve(context.Context, string) (*surface.Session, error) { return nil, nil }
func (f *fakeSurface) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	return f.observe, f.observeErr
}
func (f *fakeSurface) Send(_ context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	f.sent = append(f.sent, message)
	if f.beforeSend != nil {
		f.beforeSend()
	}
	return f.result, f.err
}
func (f *fakeSurface) SendWithOptions(ctx context.Context, session *surface.Session, message string, _ surface.SendOptions) (*surface.SendResult, error) {
	return f.Send(ctx, session, message)
}
func (*fakeSurface) Reply(context.Context, *surface.Session, int) (*surface.ReplyResult, error) {
	return nil, nil
}
func (*fakeSurface) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return nil, nil
}
func (*fakeSurface) Stream(context.Context, *surface.Session, string, func(surface.StreamEvent), time.Duration) error {
	return nil
}
func (*fakeSurface) GoalSet(context.Context, *surface.Session, string) error { return nil }
func (*fakeSurface) GoalClear(context.Context, *surface.Session) error       { return nil }
func (*fakeSurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}
func (f *fakeSurface) Compact(context.Context, *surface.Session) error {
	f.compactCalls++
	return f.err
}
func (*fakeSurface) Model(context.Context, *surface.Session, string) (string, error) { return "", nil }
func (*fakeSurface) Interrupt(context.Context, *surface.Session) error               { return nil }
func (f *fakeSurface) Steer(_ context.Context, _ *surface.Session, message string) error {
	f.steered = append(f.steered, message)
	if f.beforeSteer != nil {
		f.beforeSteer()
	}
	return f.steerErr
}
func (f *fakeSurface) Capabilities() surface.Capabilities { return f.capabilities }
func (*fakeSurface) EnsureWritable(_ context.Context, session *surface.Session) error {
	session.Transport = "managed"
	return nil
}

func TestDispatcherAcceptedAndQueued(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "s", Surface: surface.KindCodex, Name: "Build agent"}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	dispatcher := Dispatcher{Registry: r}

	receipt, err := dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{UUID: "turn", Accepted: true}}, session, "one", "")
	if err != nil || receipt.Evidence != surface.EvidenceDelivered || receipt.TurnID != "turn" || receipt.Status != string(registry.DeliveryIntentSent) || !strings.Contains(receipt.Detail, "Build agent") {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	receipt, err = dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{Accepted: false}}, session, "two", "key")
	if err != nil || receipt.Evidence != surface.EvidenceQueued || receipt.QueueID == 0 || receipt.DeliveryID == 0 || receipt.Status != string(registry.DeliveryIntentQueued) || !strings.Contains(receipt.Detail, "Build agent") || r.QueueCount("s") != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	receipt2, err := dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{Accepted: false}}, session, "two", "key")
	if err != nil || receipt2.QueueID != receipt.QueueID || r.QueueCount("s") != 1 {
		t.Fatalf("duplicate=%+v err=%v", receipt2, err)
	}
}

func TestDispatcherReturnsSubmittedForAmbiguousDeliveryWithoutClaimingAcceptance(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.RegisterSession(surface.Session{ID: "sender", Surface: surface.KindClaude}); err != nil {
		t.Fatal(err)
	}
	unknown := surface.DeliveryOutcomeUnknown(context.DeadlineExceeded)
	cases := []struct {
		name    string
		adapter *fakeSurface
	}{
		{"unknown outcome", &fakeSurface{err: unknown}},
		{"accepted then unknown", &fakeSurface{result: &surface.SendResult{UUID: "turn-uncertain", Accepted: true}, err: unknown}},
		{"empty result", &fakeSurface{result: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := &surface.Session{ID: "target-" + strings.ReplaceAll(tc.name, " ", "-"), Surface: surface.KindClaude, Name: "Claude target"}
			if err := r.RegisterSession(*session); err != nil {
				t.Fatal(err)
			}
			receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), tc.adapter, session, "do not duplicate", "", surface.SendOptions{SourceSessionID: "sender"})
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status != string(registry.DeliveryIntentSubmitted) || receipt.Evidence != surface.EvidenceSubmitted || receipt.DeliveryID == 0 || receipt.SessionID != session.ID || len(tc.adapter.sent) != 1 {
				t.Fatalf("receipt=%+v sent=%v", receipt, tc.adapter.sent)
			}
			intent, err := r.DeliveryIntent(receipt.DeliveryID)
			if err != nil || intent.Status != registry.DeliveryIntentSubmitted || intent.Evidence != surface.EvidenceSubmitted || intent.ProviderKey != "" {
				t.Fatalf("intent=%+v err=%v", intent, err)
			}
			if r.QueueCount(session.ID) != 0 {
				t.Fatal("ambiguous delivery was queued for retry")
			}
		})
	}
	if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
		t.Fatalf("ambiguous delivery became a failure: problems=%+v err=%v", problems, err)
	}
}

func TestDispatcherUnknownWithoutExplicitSenderUsesDurableOperatorAttribution(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "target", Surface: surface.KindClaude, Name: "Claude target"}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	receipt, err := (Dispatcher{Registry: r}).Deliver(context.Background(), &fakeSurface{err: surface.DeliveryOutcomeUnknown(context.DeadlineExceeded)}, session, "maybe", "")
	if err != nil || receipt == nil || receipt.DeliveryID == 0 || receipt.Status != string(registry.DeliveryIntentSubmitted) || receipt.Evidence != surface.EvidenceSubmitted {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.SenderSessionID != registry.OperatorSessionID {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherReportsTransportAcceptanceAsSentNotDelivered(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, id := range []string{"sender", "target"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude}); err != nil {
			t.Fatal(err)
		}
	}
	session := &surface.Session{ID: "target", Surface: surface.KindClaude, Name: "Claude target", Transport: "uds"}
	adapter := &sourceCheckingSurface{fakeSurface: fakeSurface{kind: surface.KindClaude, result: &surface.SendResult{UUID: "peer-envelope", Accepted: true}}}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "accepted", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != string(registry.DeliveryIntentSent) || receipt.Evidence != surface.EvidenceTransportAccepted || receipt.DeliveryID == 0 || !strings.Contains(receipt.Detail, "Claude target") {
		t.Fatalf("receipt=%+v", receipt)
	}
	if state, found, err := r.RuntimeState(session.ID); err != nil || (found && state.ActiveTurnID != "") {
		t.Fatalf("transport acceptance invented an active turn: state=%+v err=%v", state, err)
	}
}

func TestDispatcherBaselinesFirstAcceptedDelivery(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "s", Surface: surface.KindNotion}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{kind: surface.KindNotion, observe: &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "previous"}, result: &surface.SendResult{UUID: "turn", Accepted: true}}
	if _, err := (Dispatcher{Registry: r}).Deliver(context.Background(), adapter, session, "next", ""); err != nil {
		t.Fatal(err)
	}
	state, found, err := r.RuntimeState(session.ID)
	if err != nil || !found || state.ActiveTurnID != "turn" || state.CompletedTurnID != "previous" {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
}

func TestDispatcherRejectsBusyTargetWhenQueueDisabled(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "s", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithoutQueue(
		context.Background(),
		&fakeSurface{result: &surface.SendResult{Accepted: false}},
		session,
		"do not queue",
		"",
		surface.SendOptions{},
	)
	if !errors.Is(err, ErrTargetBusy) {
		t.Fatalf("expected ErrTargetBusy, got receipt=%+v err=%v", receipt, err)
	}
	if got := r.QueueCount("s"); got != 0 {
		t.Fatalf("expected no queued rows, got %d", got)
	}
	assertSynchronousRefusalLeavesNoProblem(t, r, "s", "busy")
}

func assertSynchronousRefusalLeavesNoProblem(t *testing.T, r *registry.Registry, sessionID, historyKind string) {
	t.Helper()
	if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
		t.Fatalf("synchronous refusal became a durable problem: problems=%+v err=%v", problems, err)
	}
	if got := r.QueueCount(registry.OperatorSessionID); got != 0 {
		t.Fatalf("synchronous refusal injected %d failure notices", got)
	}
	if intent, err := r.DeliveryIntent(1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("refused intent was retained: intent=%+v err=%v", intent, err)
	}
	history, err := r.ListHistory(20, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.Kind == historyKind {
			return
		}
	}
	t.Fatalf("refusal left no %q audit entry: %+v", historyKind, history)
}

type unwritableSurface struct {
	*fakeSurface
}

func (unwritableSurface) EnsureWritable(context.Context, *surface.Session) error {
	return errors.New("session is read-only")
}

func TestDispatcherSteerRefusesUnwritableSessionWithoutDurableIntent(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "read-only", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := unwritableSurface{&fakeSurface{capabilities: surface.Capabilities{Steer: true}}}
	if _, err := (Dispatcher{Registry: r}).Steer(context.Background(), adapter, session, "focus"); err == nil {
		t.Fatal("expected read-only refusal")
	}
	if len(adapter.steered) != 0 {
		t.Fatalf("read-only session was steered: %v", adapter.steered)
	}
	if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
		t.Fatalf("problems=%+v err=%v", problems, err)
	}
	if got := r.QueueCount(registry.OperatorSessionID); got != 0 {
		t.Fatalf("read-only refusal injected %d failure notices", got)
	}
	if intent, err := r.DeliveryIntent(1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherReceiptNeverClaimsMoreThanTheDurableIntent(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "thread", Name: "thread", Surface: surface.KindNotion}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{kind: surface.KindNotion, observe: &surface.TurnObservation{Status: surface.StatusUnknown}, result: &surface.SendResult{UUID: "shared-key", Accepted: true}}
	// The second send reuses the provider key, so its sent transition cannot persist.
	for _, step := range []struct {
		message string
		want    registry.DeliveryIntentStatus
	}{{"one", registry.DeliveryIntentSent}, {"two", registry.DeliveryIntentSubmitted}} {
		receipt, err := (Dispatcher{Registry: r}).Deliver(context.Background(), adapter, session, step.message, "")
		if err != nil || receipt.DeliveryID == 0 || receipt.Status != string(step.want) {
			t.Fatalf("%s: receipt=%+v err=%v", step.message, receipt, err)
		}
		intent, err := r.DeliveryIntent(receipt.DeliveryID)
		if err != nil || receipt.Status != string(intent.Status) {
			t.Fatalf("%s: receipt=%+v disagrees with durable intent=%+v err=%v", step.message, receipt, intent, err)
		}
	}
}

func TestDispatcherSteersBusyTargetWhenPolicyRequestsIt(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "busy", Name: "builder", Surface: surface.KindCodex, Status: surface.StatusBusy}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{result: &surface.SendResult{Accepted: false}, capabilities: surface.Capabilities{Steer: true}}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "interrupt with context", "", surface.SendOptions{BusyDelivery: "steer"})
	if err != nil || receipt.Evidence != surface.EvidenceDelivered || receipt.Status != string(registry.DeliveryIntentSent) || !strings.Contains(receipt.Detail, "builder") || receipt.DeliveryID <= 0 || len(adapter.steered) != 1 || r.QueueCount(session.ID) != 0 {
		t.Fatalf("receipt=%+v err=%v steered=%v queued=%d", receipt, err, adapter.steered, r.QueueCount(session.ID))
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.Status != registry.DeliveryIntentSent || intent.TargetSessionID != session.ID {
		t.Fatalf("steer intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherBusySteerUnknownKeepsSubmittedIntent(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "busy-unknown", Name: "builder", Surface: surface.KindCodex, Status: surface.StatusBusy}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	unknown := surface.DeliveryOutcomeUnknown(errors.New("steer acknowledgement lost"))
	adapter := &fakeSurface{result: &surface.SendResult{Accepted: false}, steerErr: unknown, capabilities: surface.Capabilities{Steer: true}}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "interrupt", "", surface.SendOptions{BusyDelivery: "steer"})
	if err != nil || receipt == nil || receipt.Status != string(registry.DeliveryIntentSubmitted) || receipt.DeliveryID == 0 || len(adapter.steered) != 1 {
		t.Fatalf("receipt=%+v err=%v steered=%v", receipt, err, adapter.steered)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.Status != registry.DeliveryIntentSubmitted || intent.Evidence != surface.EvidenceSubmitted {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	if got := r.QueueCount(session.ID); got != 0 {
		t.Fatalf("ambiguous steer was queued for retry: %d", got)
	}
}

func TestDispatcherDefiniteSendFailureFailsPreEffectIntent(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "failed-send", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{err: errors.New("target rejected input")}
	if _, err := (Dispatcher{Registry: r}).Deliver(context.Background(), adapter, session, "bad input", ""); err == nil {
		t.Fatal("expected provider rejection")
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil || len(problems) != 1 || problems[0].SessionID != session.ID || problems[0].Status != registry.DeliveryIntentFailed {
		t.Fatalf("problems=%+v err=%v", problems, err)
	}
	if got := r.QueueCount(registry.OperatorSessionID); got != 1 {
		t.Fatalf("expected one durable failure notice, got %d", got)
	}
}

func TestDispatcherDeliveryUnavailableLeavesNoProblemOrNotice(t *testing.T) {
	unavailable := surface.DeliveryUnavailable(errors.New("managed Codex app-server is unavailable"))
	cases := []struct {
		name string
		run  func(Dispatcher, *fakeSurface, *surface.Session) (*Receipt, error)
		fake *fakeSurface
	}{
		{"send", func(d Dispatcher, a *fakeSurface, s *surface.Session) (*Receipt, error) {
			return d.Deliver(context.Background(), a, s, "probe", "")
		}, &fakeSurface{err: unavailable}},
		{"steer", func(d Dispatcher, a *fakeSurface, s *surface.Session) (*Receipt, error) {
			return d.Steer(context.Background(), a, s, "probe")
		}, &fakeSurface{steerErr: unavailable, capabilities: surface.Capabilities{Steer: true}}},
		{"busy steer", func(d Dispatcher, a *fakeSurface, s *surface.Session) (*Receipt, error) {
			return d.DeliverWithOptions(context.Background(), a, s, "probe", "", surface.SendOptions{BusyDelivery: "steer"})
		}, &fakeSurface{result: &surface.SendResult{Accepted: false}, steerErr: unavailable, capabilities: surface.Capabilities{Steer: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			session := &surface.Session{ID: "unavailable", Surface: surface.KindCodex}
			if err := r.RegisterSession(*session); err != nil {
				t.Fatal(err)
			}
			if receipt, err := tc.run(Dispatcher{Registry: r}, tc.fake, session); !surface.IsDeliveryUnavailable(err) {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
				t.Fatalf("problems=%+v err=%v", problems, err)
			}
			if got := r.QueueCount(registry.OperatorSessionID); got != 0 {
				t.Fatalf("did-not-start refusal injected %d failure notices", got)
			}
			if intent, err := r.DeliveryIntent(1); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("intent=%+v err=%v", intent, err)
			}
			history, err := r.ListHistory(20, session.ID)
			if err != nil || len(history) == 0 || history[0].Error == "" {
				t.Fatalf("refusal left no audit entry: %+v err=%v", history, err)
			}
		})
	}
}

func TestDispatcherPersistsBeforeSendAndRetainsIDAcrossCrashBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	session := &surface.Session{ID: "send-crash", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	var seenID int64
	var callbackErr error
	var reopened *registry.Registry
	adapter := &fakeSurface{result: &surface.SendResult{UUID: "turn-after-crash", Accepted: true}}
	adapter.beforeSend = func() {
		reopened, callbackErr = registry.Open(path)
		if callbackErr != nil {
			return
		}
		var intent *registry.DeliveryIntent
		intent, callbackErr = reopened.DeliveryIntent(1)
		if callbackErr == nil {
			seenID = intent.ID
			if intent.Status != registry.DeliveryIntentSubmitted || intent.Message != "crash boundary" {
				callbackErr = fmt.Errorf("provider callback saw intent %+v", intent)
			}
		}
		_ = r.Close()
	}
	receipt, sendErr := (Dispatcher{Registry: r}).Deliver(context.Background(), adapter, session, "crash boundary", "")
	if reopened != nil {
		defer reopened.Close()
	}
	if callbackErr != nil || sendErr != nil || receipt == nil || receipt.Status != string(registry.DeliveryIntentSubmitted) || receipt.DeliveryID != seenID {
		t.Fatalf("callbackErr=%v sendErr=%v receipt=%+v seenID=%d", callbackErr, sendErr, receipt, seenID)
	}
	intent, err := reopened.DeliveryIntent(seenID)
	if err != nil || intent.Status != registry.DeliveryIntentSubmitted {
		t.Fatalf("post-crash intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherPersistsBeforeSteerAndTransitionsSameID(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "steer-callback", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	var seenID int64
	var callbackErr error
	adapter := &fakeSurface{capabilities: surface.Capabilities{Steer: true}}
	adapter.beforeSteer = func() {
		intent, err := r.DeliveryIntent(1)
		if err != nil {
			callbackErr = err
			return
		}
		seenID = intent.ID
		if intent.Status != registry.DeliveryIntentSubmitted || intent.Message != "steer callback" {
			callbackErr = fmt.Errorf("provider callback saw intent %+v", intent)
		}
	}
	receipt, err := (Dispatcher{Registry: r}).Steer(context.Background(), adapter, session, "steer callback")
	if callbackErr != nil || err != nil || receipt == nil || receipt.DeliveryID != seenID {
		t.Fatalf("callbackErr=%v err=%v receipt=%+v seenID=%d", callbackErr, err, receipt, seenID)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.Status != registry.DeliveryIntentSent {
		t.Fatalf("same-id transition intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherQueueStorageFailureDiscardsSubmittedIntent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "queue-storage-failure", Surface: surface.KindCodex, Status: surface.StatusBusy}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_test_queue_insert BEFORE INSERT ON message_queue WHEN NEW.message='reject queue' BEGIN SELECT RAISE(ABORT, 'queue insert rejected'); END`); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{result: &surface.SendResult{Accepted: false}, capabilities: surface.Capabilities{Steer: true}}
	if _, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "reject queue", "", surface.SendOptions{BusyDelivery: "queue"}); err == nil {
		t.Fatal("expected queue storage failure")
	}
	if got := r.QueueCount(session.ID); got != 0 {
		t.Fatalf("failed queue insert left target work queued: %d", got)
	}
	assertSynchronousRefusalLeavesNoProblem(t, r, session.ID, "failed")
}

func TestDispatcherDoesNotCallProviderWhenIntentPersistenceFails(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	session := &surface.Session{ID: "storage-failure", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{result: &surface.SendResult{Accepted: true}}
	if _, err := (Dispatcher{Registry: r}).Deliver(context.Background(), adapter, session, "must not send", ""); err == nil {
		t.Fatal("expected intent persistence failure")
	}
	if len(adapter.sent) != 0 {
		t.Fatalf("provider was called after intent persistence failed: %v", adapter.sent)
	}
	steerAdapter := &fakeSurface{}
	if _, err := (Dispatcher{Registry: r}).Steer(context.Background(), steerAdapter, session, "must not steer"); err == nil {
		t.Fatal("expected steer intent persistence failure")
	}
	if len(steerAdapter.steered) != 0 {
		t.Fatalf("steer provider was called after intent persistence failed: %v", steerAdapter.steered)
	}
}

func TestDispatcherSteerPreservesContextSourceInIntent(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, id := range []string{"steer-source", "steer-target"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := &fakeSurface{capabilities: surface.Capabilities{Steer: true}}
	receipt, err := (Dispatcher{Registry: r}).Steer(surface.WithSourceSessionID(context.Background(), "steer-source"), adapter, &surface.Session{ID: "steer-target", Surface: surface.KindCodex}, "focus")
	if err != nil || receipt == nil || receipt.DeliveryID == 0 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.SenderSessionID != "steer-source" || intent.Status != registry.DeliveryIntentSent {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherQueuesSteerPolicyWhenOptionsNeedPreserving(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "busy-options", Surface: surface.KindCodex, Status: surface.StatusBusy}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{result: &surface.SendResult{Accepted: false}, capabilities: surface.Capabilities{Steer: true}}
	options := surface.SendOptions{BusyDelivery: "steer", Model: "model-a", TurnOptions: surface.TurnOptions{Effort: "high"}}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "preserve options", "", options)
	if err != nil || receipt.Evidence != surface.EvidenceQueued || len(adapter.steered) != 0 || r.QueueCount(session.ID) != 1 {
		t.Fatalf("receipt=%+v err=%v steered=%v queued=%d", receipt, err, adapter.steered, r.QueueCount(session.ID))
	}
	item, err := r.QueueItem(receipt.QueueID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Model != "model-a" || item.Effort != "high" || item.BusyDelivery != "queue" {
		t.Fatalf("queued options were not preserved safely: %+v", item)
	}
}

func TestDispatcherPreflightsBusyClaudePeerBeforeTransportAcceptance(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "session_peer", Surface: surface.KindClaude, Transport: "uds", Status: surface.StatusBusy}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeSurface{kind: surface.KindClaude, observe: &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn"}, result: &surface.SendResult{Accepted: true}}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), adapter, session, "hold for peer", "", surface.SendOptions{BusyDelivery: "queue"})
	if err != nil || receipt.Evidence != surface.EvidenceQueued || len(adapter.sent) != 0 || r.QueueCount(session.ID) != 1 {
		t.Fatalf("receipt=%+v err=%v sent=%v queued=%d", receipt, err, adapter.sent, r.QueueCount(session.ID))
	}
}

func TestDispatcherCompactUsesTypedSurfaceOperation(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	claudeSession := &surface.Session{ID: "claude", Surface: surface.KindClaude, Status: surface.StatusBusy}
	codexSession := &surface.Session{ID: "codex", Surface: surface.KindCodex, Status: surface.StatusIdle}
	for _, session := range []*surface.Session{claudeSession, codexSession} {
		if err := r.RegisterSession(*session); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher := Dispatcher{Registry: r}
	claude := &fakeSurface{kind: surface.KindClaude}
	receipt, err := dispatcher.Compact(context.Background(), claude, claudeSession)
	if err != nil || receipt.Evidence != surface.EvidenceQueued || receipt.QueueID == 0 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if r.QueueCount(claudeSession.ID) != 1 || claude.compactCalls != 0 || len(claude.sent) != 0 {
		t.Fatalf("sent=%v compactCalls=%d queued=%d", claude.sent, claude.compactCalls, r.QueueCount(claudeSession.ID))
	}
	codex := &fakeSurface{kind: surface.KindCodex}
	receipt, err = dispatcher.Compact(context.Background(), codex, codexSession)
	if err != nil || receipt.Evidence != surface.EvidenceDelivered || codex.compactCalls != 1 || len(codex.sent) != 0 {
		t.Fatalf("receipt=%+v sent=%v compactCalls=%d err=%v", receipt, codex.sent, codex.compactCalls, err)
	}
}

func TestDispatcherCompactReportsTypedControlFailure(t *testing.T) {
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude}
	adapter := &fakeSurface{kind: surface.KindClaude, err: errors.New("control unavailable")}
	receipt, err := (Dispatcher{}).Compact(context.Background(), adapter, session)
	if err == nil || !strings.Contains(err.Error(), "control unavailable") || receipt != nil || len(adapter.sent) != 0 || adapter.compactCalls != 1 {
		t.Fatalf("receipt=%+v sent=%v err=%v", receipt, adapter.sent, err)
	}
}
