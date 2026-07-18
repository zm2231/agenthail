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
func (*fakeSurface) Steer(context.Context, *surface.Session, string) error           { return nil }
func (*fakeSurface) Capabilities() surface.Capabilities                              { return surface.Capabilities{} }

func TestDispatcherAcceptedAndQueued(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := &surface.Session{ID: "s", Surface: surface.KindCodex}
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	dispatcher := Dispatcher{Registry: r}

	receipt, err := dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{UUID: "turn", Accepted: true}}, session, "one", "")
	if err != nil || receipt.Disposition != DispositionAccepted || receipt.TurnID != "turn" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	receipt, err = dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{Accepted: false}}, session, "two", "key")
	if err != nil || receipt.Disposition != DispositionQueued || receipt.QueueID == 0 || r.QueueCount("s") != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	receipt2, err := dispatcher.Deliver(context.Background(), &fakeSurface{result: &surface.SendResult{Accepted: false}}, session, "two", "key")
	if err != nil || receipt2.QueueID != receipt.QueueID || r.QueueCount("s") != 1 {
		t.Fatalf("duplicate=%+v err=%v", receipt2, err)
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

func TestDispatcherCompactUsesClaudeCommandDeliveryAndCodexNativeOperation(t *testing.T) {
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
	claude := &fakeSurface{
		kind:    surface.KindClaude,
		observe: &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "active"},
		result:  &surface.SendResult{Accepted: false},
	}
	receipt, err := dispatcher.Compact(context.Background(), claude, claudeSession)
	if err != nil || receipt.Disposition != DispositionQueued || receipt.QueueID == 0 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	item, err := r.QueueItem(receipt.QueueID)
	if err != nil || item.Message != "/compact" || claude.compactCalls != 0 || len(claude.sent) != 1 || claude.sent[0] != "/compact" {
		t.Fatalf("item=%+v sent=%v compactCalls=%d err=%v", item, claude.sent, claude.compactCalls, err)
	}
	codex := &fakeSurface{kind: surface.KindCodex}
	receipt, err = dispatcher.Compact(context.Background(), codex, codexSession)
	if err != nil || receipt.Disposition != DispositionAccepted || codex.compactCalls != 1 || len(codex.sent) != 0 {
		t.Fatalf("receipt=%+v sent=%v compactCalls=%d err=%v", receipt, codex.sent, codex.compactCalls, err)
	}
}

func TestDispatcherCompactRejectsUnobservableClaudeSession(t *testing.T) {
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude}
	adapter := &fakeSurface{kind: surface.KindClaude, observeErr: errors.New("transcript unavailable")}
	receipt, err := (Dispatcher{}).Compact(context.Background(), adapter, session)
	if err == nil || !strings.Contains(err.Error(), "observe before compact") || receipt != nil || len(adapter.sent) != 0 {
		t.Fatalf("receipt=%+v sent=%v err=%v", receipt, adapter.sent, err)
	}
}
