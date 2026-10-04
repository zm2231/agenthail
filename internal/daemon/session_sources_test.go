package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sourceCountingSurface struct {
	*daemonSurface
	calls     atomic.Int32
	started   chan struct{}
	events    chan surface.StreamEvent
	items     []surface.TimelineItem
	streamErr error
}

type restartingSource struct {
	*daemonSurface
	calls atomic.Int32
}

func TestSessionJournalInlineBodyKeepsUTF8Boundary(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(reg)
	source := &sessionSource{manager: manager, session: &from, adapter: fake, epoch: "test", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	body := strings.Repeat("a", sessionStreamBodyBytes-1) + "é rest"
	source.append(surface.StreamEvent{ID: "unicode", Kind: "text", Text: body})
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(page.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(payload.Body) || strings.ContainsRune(payload.Body, utf8.RuneError) || !strings.HasPrefix(body, payload.Body) || !payload.Truncated || payload.BodyRef == "" || payload.TruncationReason != "" {
		t.Fatalf("invalid bounded body: %+v", payload)
	}
	full, _, err := reg.SessionJournalBody(from.ID, payload.BodyRef, 0, len(body))
	if err != nil || string(full) != body {
		t.Fatalf("full body mismatch: err=%v", err)
	}
}

func TestSessionJournalBodyBudgetKeepsPreviewWithoutDeadReference(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(reg)
	source := &sessionSource{manager: manager, session: &from, adapter: fake, epoch: "test", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	body := strings.Repeat("x", sessionJournalRetentionBytes)
	source.append(surface.StreamEvent{ID: "oversized", Kind: "text", Text: body})
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(page.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Body == "" || !strings.HasPrefix(body, payload.Body) || !payload.Truncated || payload.TruncationReason != "full_body_not_retained" || payload.BodyRef != "" {
		t.Fatalf("payload=%+v", payload)
	}
	if len(payload.Body) > sessionStreamBodyBytes {
		t.Fatalf("inline preview bytes=%d, want <=%d", len(payload.Body), sessionStreamBodyBytes)
	}
}

func (s *restartingSource) Stream(ctx context.Context, _ *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	if s.calls.Add(1) == 1 {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
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
	if s.streamErr != nil {
		return s.streamErr
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

func TestSessionSourcePersistsStreamFailureAsReset(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		streamErr:     errors.New(strings.Repeat("upstream unavailable ", 32)),
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	var entry registry.SessionJournalEntry
	select {
	case entry = <-subscription.Entries:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive source failure")
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if entry.Seq == 0 || payload.Op != "reset" || payload.Kind != "source-error" || payload.ItemID == "" || payload.Reason == "" || len([]rune(payload.Reason)) > 240 {
		t.Fatalf("entry=%+v payload=%+v", entry, payload)
	}
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 || window.Entries[0].Seq != entry.Seq {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

func TestSessionSourceSeedsJournalBeforeSubscribeReturns(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "seed-1", Kind: "text", Text: "seeded"}},
	}
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ItemID != "seed-1" || payload.Body != "seeded" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestSessionSourceSeedsWithoutUnsupportedLiveStream(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindClaude
	from.Transport = "uds"
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "seed-uds", Kind: "text", Text: "peer seed"}},
	}
	adapter.caps.Stream = false
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ItemID != "seed-uds" || payload.Kind == "source-error" || adapter.calls.Load() != 0 {
		t.Fatalf("payload=%+v streamCalls=%d", payload, adapter.calls.Load())
	}
}

func TestSessionSourceSharesOneUpstreamAndJournalsNormalizedEvents(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
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

func TestSessionSourceAccumulatesStableProviderAppendIntoOneJournalEntry(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 2)}
	adapter.caps.Stream = true
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
	adapter.events <- surface.StreamEvent{ID: "managed:turn-1:text", ProviderKey: "managed:turn-1:text", Version: 3, Operation: "append", TurnID: "turn-1", Kind: "text", Text: "hel"}
	adapter.events <- surface.StreamEvent{ID: "managed:turn-1:text", ProviderKey: "managed:turn-1:text", Version: 5, Operation: "append", TurnID: "turn-1", Kind: "text", Text: "lo"}
	for range 2 {
		select {
		case <-subscription.Entries:
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
	window, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil || payload.ItemID != "managed:turn-1:text" || payload.Body != "hello" || payload.Op != "upsert" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}

func TestSessionSourceAssignsIdentityToProviderEventsWithoutOne(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(registry)
	source := &sessionSource{manager: manager, session: &from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	first := source.normalizeLocked(surface.StreamEvent{Kind: "context"})
	second := source.normalizeLocked(surface.StreamEvent{Kind: "context"})
	if first.ItemID == "" || first.ProviderKey == "" || second.ItemID == "" || first.ItemID == second.ItemID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestSessionSourceNormalizesGoalUpdatesAndClears(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(registry)
	source := &sessionSource{manager: manager, session: &from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	budget := int64(500)
	updated := source.normalizeLocked(surface.StreamEvent{ID: "goal:1", ProviderKey: "goal:1", Kind: "goal", Operation: "replace", Goal: &surface.GoalState{Objective: "verify", Status: surface.GoalStatusPaused, TimeUsedSeconds: 12, TokensUsed: 34, TokenBudget: &budget}})
	encoded, err := json.Marshal(updated)
	if err != nil || updated.Goal == nil || updated.Goal.Status != surface.GoalStatusPaused || *updated.Goal.TokenBudget != 500 || !strings.Contains(string(encoded), `"goal"`) {
		t.Fatalf("updated=%+v encoded=%s err=%v", updated, encoded, err)
	}
	cleared := source.normalizeLocked(surface.StreamEvent{ID: "goal:2", ProviderKey: "goal:2", Kind: "goal", Operation: "replace"})
	clearedJSON, err := json.Marshal(cleared)
	if err != nil || !strings.Contains(string(clearedJSON), `"goal":null`) {
		t.Fatalf("cleared=%+v json=%s err=%v", cleared, clearedJSON, err)
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
	adapter.caps.Stream = true
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
	_, held := d.sourceHolds[from.ID]["active-turn"]
	d.sourceHoldMu.Unlock()
	if !held {
		t.Fatal("active turn source was not held")
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(context.Background(), adapter, &from)
	d.sourceHoldMu.Lock()
	_, held = d.sourceHolds[from.ID]["active-turn"]
	d.sourceHoldMu.Unlock()
	if held {
		t.Fatal("idle session source remained held")
	}
}

func TestSourceHoldsAreReferenceCountedByOwner(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent)}
	adapter.caps.Stream = true
	d.Surfaces = []surface.Surface{adapter}
	d.setSessionSourceHold(&from, true, "active-turn")
	d.setSessionSourceHold(&from, true, "voice")
	d.setSessionSourceHold(&from, false, "voice")
	d.sourceHoldMu.Lock()
	_, active := d.sourceHolds[from.ID]["active-turn"]
	_, voice := d.sourceHolds[from.ID]["voice"]
	d.sourceHoldMu.Unlock()
	if !active || voice {
		t.Fatalf("active=%v voice=%v", active, voice)
	}
	d.setSessionSourceHold(&from, false, "active-turn")
}

func TestHeldSourceRestartsAfterUpstreamEnds(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &restartingSource{daemonSurface: fake}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	release, err := manager.hold(&from, adapter, "active-turn")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	deadline := time.Now().Add(2 * time.Second)
	for adapter.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls := adapter.calls.Load(); calls < 2 {
		t.Fatalf("stream calls=%d", calls)
	}
}
