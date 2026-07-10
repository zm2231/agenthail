package delivery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type fakeSurface struct {
	result *surface.SendResult
	err    error
}

func (*fakeSurface) Name() surface.SurfaceKind                                 { return surface.KindCodex }
func (*fakeSurface) List(context.Context) ([]surface.Session, error)           { return nil, nil }
func (*fakeSurface) Resolve(context.Context, string) (*surface.Session, error) { return nil, nil }
func (*fakeSurface) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	return nil, nil
}
func (f *fakeSurface) Send(context.Context, *surface.Session, string) (*surface.SendResult, error) {
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
func (*fakeSurface) Compact(context.Context, *surface.Session) error                 { return nil }
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
