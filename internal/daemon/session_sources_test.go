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
	items   []surface.TimelineItem
}

func (s *sourceCountingSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{Items: append([]surface.TimelineItem(nil), s.items...)}, nil
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

func TestSessionSourceSeedsBoundedTimelineBeforeStreaming(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items: []surface.TimelineItem{{
			ID:        "timeline-1",
			Kind:      "message",
			Text:      "persisted activity",
			Timestamp: "2026-10-03T12:00:00Z",
		}},
	}
	manager := newSessionSourceManager(registry)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	window, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Entries) != 1 {
		t.Fatalf("seeded entries=%d", len(window.Entries))
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ItemID != "timeline-1" || payload.ProviderKey != "timeline:timeline-1" || payload.Op != "upsert" || payload.Body != "persisted activity" {
		t.Fatalf("seed payload=%+v", payload)
	}
}

func TestObservationHoldsActiveTurnSourceUntilIdle(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent)}
	adapter.caps.Stream = true
	d.Surfaces = []surface.Surface{adapter}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	d.observeSession(context.Background(), adapter, &from)
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("active turn did not hold a source")
	}
	d.sourceHoldMu.Lock()
	_, held := d.sourceHolds[from.ID]
	d.sourceHoldMu.Unlock()
	if !held {
		t.Fatal("active turn source was not held")
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(context.Background(), adapter, &from)
	d.sourceHoldMu.Lock()
	_, held = d.sourceHolds[from.ID]
	d.sourceHoldMu.Unlock()
	if held {
		t.Fatal("idle session source remained held")
	}
}
