package delivery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type fakeSurface struct {
	kind         surface.SurfaceKind
	result       *surface.SendResult
	err          error
	observe      *surface.TurnObservation
	observeErr   error
	sent         []string
	steered      []string
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
	return nil
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
	if err != nil || receipt.Evidence != surface.EvidenceDelivered || receipt.TurnID != "turn" || receipt.Detail != "Sent to Build agent." {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	receipt, err = dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{Accepted: false}}, session, "two", "key")
	if err != nil || receipt.Evidence != surface.EvidenceQueued || receipt.QueueID == 0 || receipt.Detail != "Queued for Build agent; sends when current turn ends." || r.QueueCount("s") != 1 {
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
	for _, id := range []string{"sender", "target"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude}); err != nil {
			t.Fatal(err)
		}
	}
	session := &surface.Session{ID: "target", Surface: surface.KindClaude, Name: "Claude target"}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), &fakeSurface{err: surface.DeliveryOutcomeUnknown(context.DeadlineExceeded)}, session, "do not duplicate", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != string(registry.DeliveryIntentSubmitted) || receipt.Evidence != surface.EvidenceSubmitted || receipt.DeliveryID == 0 || receipt.Detail != "Submitted to Claude target." {
		t.Fatalf("receipt=%+v", receipt)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.Status != registry.DeliveryIntentSubmitted || intent.Evidence != surface.EvidenceSubmitted {
		t.Fatalf("intent=%+v err=%v", intent, err)
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
	if err != nil || receipt == nil || receipt.DeliveryID == 0 || receipt.Status != string(registry.DeliveryIntentSubmitted) || receipt.Detail != "Submitted to Claude target." {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.SenderSessionID != registry.OperatorSessionID {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestDispatcherQueuedReceiptBindsDeliveryIntentToQueue(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, id := range []string{"sender", "target"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
	session := &surface.Session{ID: "target", Surface: surface.KindCodex, Name: "Codex target"}
	receipt, err := (Dispatcher{Registry: r}).DeliverWithOptions(context.Background(), &fakeSurface{result: &surface.SendResult{Accepted: false}}, session, "later", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil || receipt.Status != string(registry.DeliveryIntentQueued) || receipt.DeliveryID == 0 || receipt.Detail != "Queued for Codex target; sends when current turn ends." {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil || intent.QueueID != receipt.QueueID || intent.Status != registry.DeliveryIntentQueued {
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
	if receipt.Status != string(registry.DeliveryIntentSent) || receipt.Evidence != surface.EvidenceTransportAccepted || receipt.DeliveryID == 0 || receipt.Detail != "Sent to Claude target." {
		t.Fatalf("receipt=%+v", receipt)
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
	if err != nil || receipt.Evidence != surface.EvidenceDelivered || receipt.Detail != "Sent to builder." || len(adapter.steered) != 1 || r.QueueCount(session.ID) != 0 {
		t.Fatalf("receipt=%+v err=%v steered=%v queued=%d", receipt, err, adapter.steered, r.QueueCount(session.ID))
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

func TestDispatcherRecordsUnknownOutcomeAsUnknownEvidence(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude, Status: surface.StatusIdle}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	unknown := surface.DeliveryOutcomeUnknown(errors.New("sidecar not found"))
	adapter := &fakeSurface{kind: surface.KindClaude, err: unknown}
	dispatcher := Dispatcher{Registry: r}
	submitted, err := dispatcher.Deliver(context.Background(), adapter, session, "hello", "")
	if err != nil || submitted.Evidence != surface.EvidenceSubmitted || submitted.Status != string(registry.DeliveryIntentSubmitted) {
		t.Fatalf("submitted=%+v err=%v", submitted, err)
	}
	receipt, err := dispatcher.Compact(context.Background(), adapter, session)
	if err != nil || receipt.Evidence != surface.EvidenceQueued {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	entries, err := r.ListHistory(10, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
	if entries[0].Evidence != surface.EvidenceQueued || entries[1].Evidence != surface.EvidenceSubmitted {
		t.Fatalf("entries=%+v", entries)
	}
}
