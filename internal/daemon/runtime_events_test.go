package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type timelineDaemonSurface struct {
	*daemonSurface
	mu    sync.Mutex
	pages map[int64]*surface.SessionTimeline
}

func (s *timelineDaemonSurface) Timeline(_ context.Context, _ *surface.Session, before int64) (*surface.SessionTimeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pages[before], nil
}

func (s *timelineDaemonSurface) SetPage(before int64, page *surface.SessionTimeline) {
	s.mu.Lock()
	s.pages[before] = page
	s.mu.Unlock()
}

func TestPublishTimelineEventsPaginatesAndDeduplicatesAcrossRestart(t *testing.T) {
	d, r, _, _, target := daemonFixture(t)
	target.Source = "agenthail"
	target.Transport = "managed"
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	adapter := &timelineDaemonSurface{
		daemonSurface: &daemonSurface{kind: surface.KindCodex, caps: surface.Capabilities{Stream: true}},
		pages: map[int64]*surface.SessionTimeline{
			0:  {NextBefore: 10, Items: []surface.TimelineItem{{ID: "new-message", Kind: "message", Role: "assistant", Text: "answer"}, {ID: "new-tool", Kind: "toolCall", Title: "shell", CallID: "tool-1", Text: `{"cmd":"pwd"}`}}},
			10: {Items: []surface.TimelineItem{{ID: "old-thought", Kind: "reasoning", Text: "plan"}, {ID: "old-result", Kind: "toolResult", Title: "shell", CallID: "tool-1", Text: `{"code":0}`}}},
		},
	}
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	if got := len(d.events.history); got != 4 {
		t.Fatalf("published events=%d want=4", got)
	}
	for _, event := range d.events.history {
		var payload canonicalRuntimeEvent
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Type == "" || payload.ID == "" {
			t.Fatalf("invalid canonical event=%s", event.Data)
		}
	}
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	restarted := newEventHub(r)
	if len(restarted.history) != 4 {
		t.Fatalf("restarted events=%d want=4", len(restarted.history))
	}
	if len(d.events.history) != 4 {
		t.Fatalf("duplicate events after refresh=%d want=4", len(d.events.history))
	}
}

func TestPublishTimelineEventsMarksStoredTruncationWithoutChunkSemantics(t *testing.T) {
	d, r, _, _, target := daemonFixture(t)
	target.Source = "agenthail"
	target.Transport = "managed"
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	adapter := &timelineDaemonSurface{
		daemonSurface: &daemonSurface{kind: surface.KindCodex, caps: surface.Capabilities{Stream: true}},
		pages:         map[int64]*surface.SessionTimeline{0: {Items: []surface.TimelineItem{{ID: "truncated-message", Kind: "message", Role: "assistant", Text: "partial storage", Truncated: true}}}},
	}
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	if len(d.events.history) != 1 {
		t.Fatalf("history=%d want=1", len(d.events.history))
	}
	var payload map[string]any
	if err := json.Unmarshal(d.events.history[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["truncated"] != true {
		t.Fatalf("payload=%v missing truncated marker", payload)
	}
	if _, ok := payload["chunk"]; ok {
		t.Fatalf("payload=%v incorrectly uses streaming chunk semantics", payload)
	}
}

func TestPublishTimelineEventsKeepsItemsBeforeProcessedPageBoundary(t *testing.T) {
	d, r, _, _, target := daemonFixture(t)
	target.Source = "agenthail"
	target.Transport = "managed"
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	adapter := &timelineDaemonSurface{
		daemonSurface: &daemonSurface{kind: surface.KindCodex, caps: surface.Capabilities{Stream: true}},
		pages: map[int64]*surface.SessionTimeline{
			0:  {Items: []surface.TimelineItem{{ID: "boundary-old", Kind: "message", Role: "assistant", Text: "old"}}},
			10: {Items: []surface.TimelineItem{{ID: "older-unseen", Kind: "message", Role: "assistant", Text: "older"}}},
		},
	}
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	adapter.SetPage(0, &surface.SessionTimeline{NextBefore: 10, Items: []surface.TimelineItem{
		{ID: "boundary-new", Kind: "message", Role: "assistant", Text: "new"},
		{ID: "boundary-old", Kind: "message", Role: "assistant", Text: "old"},
	}})
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	if got := len(d.events.history); got != 2 {
		t.Fatalf("history=%d want=2", got)
	}
	for _, event := range d.events.history {
		if string(event.Data) != `{"type":"message","sessionId":"to","id":"boundary-old","role":"assistant","text":"old"}` && string(event.Data) != `{"type":"message","sessionId":"to","id":"boundary-new","role":"assistant","text":"new"}` {
			t.Fatalf("unexpected event=%s", event.Data)
		}
	}
}

func TestPublishTimelineEventsDoesNotRequeuePrunedHistory(t *testing.T) {
	d, r, _, _, target := daemonFixture(t)
	target.Source = "agenthail"
	target.Transport = "managed"
	if err := r.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	items := make([]surface.TimelineItem, 1100)
	for i := range items {
		items[i] = surface.TimelineItem{ID: fmt.Sprintf("message-%d", i), Kind: "message", Role: "assistant", Text: "complete"}
	}
	adapter := &timelineDaemonSurface{daemonSurface: &daemonSurface{kind: surface.KindCodex, caps: surface.Capabilities{Stream: true}}, pages: map[int64]*surface.SessionTimeline{0: {Items: items}}}
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	if got := len(d.events.history); got != eventHistoryLimit {
		t.Fatalf("event history=%d want=%d", got, eventHistoryLimit)
	}
	if err := d.publishTimelineEvents(context.Background(), adapter, &target); err != nil {
		t.Fatal(err)
	}
	if got := len(d.events.history); got != eventHistoryLimit {
		t.Fatalf("refresh republished pruned items; history=%d", got)
	}
	if got := len(newEventHub(r).history); got != eventHistoryLimit {
		t.Fatalf("restart history=%d want=%d", got, eventHistoryLimit)
	}
}
