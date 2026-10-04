package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type coldPageSurface struct {
	*daemonSurface
	readCalls atomic.Int32
	started   chan struct{}
}

func TestDashboardSnapshotCacheRejectsPreviousHostEpoch(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	epoch, seq, err := reg.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	dashboard := &dashboardServer{state: dashboardState{HostEpoch: "previous-epoch", CatalogSeq: seq}, stateAt: time.Now()}
	response := httptest.NewRecorder()
	d.dashboardStateCached(dashboard, response, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	var state dashboardState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatalf("status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	if state.HostEpoch != epoch {
		t.Fatalf("cached previous epoch: %+v, want %s", state, epoch)
	}
}

func (s *coldPageSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.readCalls.Add(1)
	return &surface.SessionReadResult{Items: []surface.TimelineItem{{ID: "seeded-item", Kind: "text", Role: "assistant", BodyRef: "seed-ref", Text: "seeded from provider"}}}, nil
}

func (s *coldPageSurface) Stream(ctx context.Context, _ *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestDashboardSessionColdConcurrentReadsShareOneJournalSeed(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	adapter := &coldPageSurface{daemonSurface: fake, started: make(chan struct{}, 1)}
	d.Surfaces = []surface.Surface{adapter}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	type result struct {
		status int
		body   string
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			request := httptest.NewRequest(http.MethodGet, "/api/session?id="+from.ID+"&timeline=1", nil)
			request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			results <- result{status: response.Code, body: response.Body.String()}
		}()
	}
	close(start)
	for range 2 {
		select {
		case got := <-results:
			if got.status != http.StatusOK || !contains(got.body, "seeded from provider") || !contains(got.body, `"readSource":"journal"`) {
				t.Fatalf("cold response status=%d body=%s", got.status, got.body)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cold concurrent session reads did not complete")
		}
	}
	if got := adapter.readCalls.Load(); got != 1 {
		t.Fatalf("provider seed reads=%d, want exactly one", got)
	}
}

func TestSessionPageHandoffReusesSeedWithoutJournalChurn(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &coldPageSurface{daemonSurface: fake, started: make(chan struct{}, 1)}
	adapter.caps.Stream = true
	t.Cleanup(d.sources.shutdown)
	if err := d.sources.seed(context.Background(), &from, adapter); err != nil {
		t.Fatal(err)
	}
	first, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := d.sources.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	second, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || adapter.readCalls.Load() != 1 || first.LatestSeq != second.LatestSeq {
		t.Fatalf("reads=%d first=%+v second=%+v err=%v", adapter.readCalls.Load(), first, second, err)
	}
}

func TestReadJournalPagePreservesPagingIdentityRolesAndBodyReferences(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	retention := registry.SessionJournalRetention{Count: 32, Bytes: 16 << 10}
	for index := 1; index <= 5; index++ {
		payload := sessionJournalPayload{
			ItemID:  "item-" + string(rune('0'+index)),
			Version: 1,
			Op:      "upsert",
			Kind:    "text",
			Role:    "assistant",
			Title:   "Answer",
			Body:    "body-" + string(rune('0'+index)),
			BodyRef: "body-ref-" + string(rune('0'+index)),
			TS:      time.Date(2026, 10, 4, 12, index, 0, 0, time.UTC).Format(time.RFC3339),
		}
		if index == 1 {
			payload.Role = "user"
			payload.Title = "Prompt"
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: payload.Kind, ProviderKey: payload.ItemID, Payload: encoded, BodyRef: payload.BodyRef, FullBody: []byte(payload.Body), ObservedAt: time.Unix(int64(index), 0)}, retention); err != nil {
			t.Fatal(err)
		}
	}

	first, err := d.readJournalPage(from.ID, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if first.Source != "journal" || first.JournalSeq != 5 || first.NextBefore != 2 || len(first.Items) != 4 {
		t.Fatalf("first=%+v", first)
	}
	for index, item := range first.Items {
		wantID := "item-" + string(rune('2'+index))
		if item.ID != wantID || item.Role != "assistant" || item.BodyRef != "body-ref-"+string(rune('2'+index)) {
			t.Fatalf("item[%d]=%+v want id=%s", index, item, wantID)
		}
	}

	second, err := d.readJournalPage(from.ID, uint64(first.NextBefore), 4)
	if err != nil {
		t.Fatal(err)
	}
	if second.JournalSeq != 5 || second.NextBefore != 0 || len(second.Items) != 1 || second.Items[0].ID != "item-1" || second.Items[0].Role != "user" || second.Items[0].BodyRef != "body-ref-1" {
		t.Fatalf("second=%+v", second)
	}
	if len(second.Exchanges) != 1 || second.Exchanges[0].User != "body-1" || second.Exchanges[0].Source != "journal" {
		t.Fatalf("exchanges=%+v", second.Exchanges)
	}
}

func TestReadJournalPageKeepsToolAndReasoningRolesOutOfChatExchanges(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	items := []sessionJournalPayload{
		{ItemID: "prompt", Kind: "message", Role: "user", Body: "Question"},
		{ItemID: "thinking", Kind: "reasoning", Role: "assistant", Body: "Private reasoning"},
		{ItemID: "call", Kind: "toolCall", Role: "assistant", Body: "Tool input"},
		{ItemID: "result", Kind: "toolResult", Role: "user", Body: "Tool output"},
		{ItemID: "image", Kind: "attachment", Role: "user", Body: "Image attachment"},
		{ItemID: "answer", Kind: "message", Role: "assistant", Body: "Answer"},
	}
	for _, item := range items {
		item.Op = "upsert"
		item.Version = 1
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: item.Kind, ProviderKey: item.ItemID, Payload: encoded, ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: 32, Bytes: 16 << 10}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != len(items) || len(page.Exchanges) != 1 || page.Exchanges[0].User != "Question" || page.Exchanges[0].Assistant != "Answer" {
		t.Fatalf("page lost timeline or mixed tool/reasoning into chat: %+v", page)
	}
}

func TestDesktopStreamIdentityPreservesJournalAndPageBoundaries(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	retention := registry.SessionJournalRetention{Count: 32, Bytes: 16 << 10}
	appendPayload := func(payload sessionJournalPayload) {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{
			SessionID:   from.ID,
			Kind:        payload.Kind,
			ProviderKey: payload.ItemID,
			Payload:     encoded,
			ObservedAt:  time.Now(),
		}, retention); err != nil {
			t.Fatal(err)
		}
	}
	appendPayload(sessionJournalPayload{ItemID: "user-2", Kind: "message", Role: "user", Body: "next question", Op: "upsert", Version: 1})
	appendPayload(sessionJournalPayload{ItemID: "codex-turn-1-assistant-assistant-1", Kind: "text", Role: "assistant", Body: "hel", Op: "upsert", Version: 1})
	appendPayload(sessionJournalPayload{ItemID: "codex-turn-1-toolCall-tool-1", Kind: "toolCall", Role: "assistant", CallID: "tool-1", Body: "lookup", Op: "upsert", Version: 1})
	appendPayload(sessionJournalPayload{ItemID: "codex-turn-1-assistant-assistant-1", Kind: "text", Role: "assistant", Body: "authoritative answer", Op: "upsert", Version: 2})
	appendPayload(sessionJournalPayload{ItemID: "codex-turn-2-assistant-assistant-2", Kind: "text", Role: "assistant", Body: "second answer", Op: "upsert", Version: 1})

	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("items=%+v", page.Items)
	}
	if page.Items[1].ID != "codex-turn-1-toolCall-tool-1" || page.Items[1].CallID != "tool-1" || page.Items[2].ID != "codex-turn-1-assistant-assistant-1" || page.Items[2].Text != "authoritative answer" {
		t.Fatalf("first turn page items=%+v", page.Items)
	}
	if len(page.Exchanges) != 2 || page.Exchanges[0].User != "next question" || page.Exchanges[0].Assistant != "authoritative answer" || page.Exchanges[1].Assistant != "second answer" {
		t.Fatalf("exchanges=%+v", page.Exchanges)
	}
}

func TestDashboardSessionReturnsTypedHistoryGapAfterJournalPrune(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	retention := registry.SessionJournalRetention{Count: 2, Bytes: 1024}
	for _, key := range []string{"one", "two", "three"} {
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: key, Payload: []byte(key)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id="+from.ID+"&timeline=1&timelineBefore=2", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		EarliestSeq uint64 `json:"earliestSeq"`
		LatestSeq   uint64 `json:"latestSeq"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "history_gap" || body.EarliestSeq != 2 || body.LatestSeq != 3 {
		t.Fatalf("body=%+v", body)
	}
}

func TestReadJournalPageSkipsSourceErrorsWithoutProviderFallback(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	payload := []byte(`{"itemId":"source-error","version":1,"op":"upsert","kind":"source-error","body":"private detail"}`)
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "source-error", ProviderKey: "source-error", Payload: payload}, registry.SessionJournalRetention{Count: 4, Bytes: 4096}); err != nil {
		t.Fatal(err)
	}
	result, err := d.readJournalPage(from.ID, 0, 4)
	if err != nil || result.Source != "journal" || len(result.Items) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
