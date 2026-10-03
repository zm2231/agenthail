package daemon

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type sourceCountingSurface struct {
	*daemonSurface
	calls   atomic.Int32
	started chan struct{}
	events  chan surface.StreamEvent
}

func (s *sourceCountingSurface) Stream(ctx context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	s.calls.Add(1)
	select {
	case s.started <- struct{}{}:
	default:
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-s.events:
			onEvent(event)
		}
	}
}

func TestSessionSourceSharesOneUpstreamAndJournalsNormalizedEvents(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	manager := newSessionSourceManager(registry)
	first, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	third, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cancel()
	defer second.Cancel()
	defer third.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("upstream stream calls=%d", calls)
	}
	adapter.events <- surface.StreamEvent{ID: "m1", ProviderKey: "m1", Version: 4, Operation: "append", TurnID: "t1", Kind: "text", Text: "test"}
	for _, subscription := range []sessionSourceSubscription{first, second, third} {
		select {
		case entry := <-subscription.Entries:
			var payload sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if entry.Seq != 1 || payload.ItemID != "m1" || payload.ProviderKey != "m1" || payload.Op != "upsert" || payload.Body != "test" || payload.TurnID != "t1" {
				t.Fatalf("entry=%+v payload=%+v", entry, payload)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
	window, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}
