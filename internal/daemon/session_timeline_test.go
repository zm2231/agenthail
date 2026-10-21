package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	registrypkg "github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type timelineSurface struct {
	*daemonSurface
	cursor int64
	fail   bool
}

func (s *timelineSurface) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.cursor = request.Before
	if s.fail {
		return nil, errors.New("private local path")
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{{ID: "tool-1", Kind: "toolCall", Title: "Bash", Text: "go test", CallID: "call-1"}}, Exchanges: []surface.Exchange{}, NextBefore: 123, Source: "local-transcript"}, nil
}
func TestAPIV1MobileTimelinePreservesContractAndReadAuthorization(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &timelineSurface{daemonSurface: fake}
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	turns := []struct{ id, role, turn string }{
		{"item-1", "user", "turn-1"}, {"item-2", "assistant", "turn-1"},
		{"item-3", "user", "turn-2"}, {"item-4", "assistant", "turn-2"},
		{"item-5", "user", "turn-3"}, {"item-6", "assistant", "turn-3"},
	}
	for _, item := range turns {
		number := strings.TrimPrefix(item.id, "item-")
		payload := `{"itemId":"` + item.id + `","version":1,"op":"upsert","kind":"text","role":"` + item.role + `","turnId":"` + item.turn + `","body":"body-` + number + `","bodyRef":"ref-` + number + `"}`
		if _, _, err := registry.AppendSessionJournalEntry(registrypkg.SessionJournalEntry{SessionID: "from", Kind: "text", ProviderKey: item.id, Payload: []byte(payload), BodyRef: "ref-" + number, FullBody: []byte("body-" + number)}, registrypkg.SessionJournalRetention{Count: 32, Bytes: 4096}); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Exchanges []surface.Exchange      `json:"exchanges"`
		Timeline  surface.SessionTimeline `json:"timeline"`
	}
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&limit=2", nil)
		if authenticated {
			request.Header.Set("Authorization", "Bearer secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !authenticated {
			if response.Code != http.StatusUnauthorized {
				t.Fatal(response.Code)
			}
			continue
		}
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var body page
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Timeline.Source != "journal" || body.Timeline.NextBefore != 3 || len(body.Timeline.Items) != 5 || body.Timeline.Items[4].ID != "tool-1" || body.Timeline.Items[0].ID != "item-3" || body.Timeline.Items[0].Role != "user" || body.Timeline.Items[0].BodyRef != "ref-3" {
			t.Fatalf("%+v", body)
		}
		if len(body.Exchanges) != 2 || body.Exchanges[0].User != "body-3" || body.Exchanges[0].Assistant != "body-4" || body.Exchanges[1].User != "body-5" || body.Exchanges[1].Assistant != "body-6" {
			t.Fatalf("exchanges=%+v", body.Exchanges)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&timelineBefore=3&limit=2", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body page
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || body.Timeline.Source != "journal" || len(body.Timeline.Items) != 2 || body.Timeline.Items[0].ID != "item-1" || body.Timeline.Items[0].Role != "user" || body.Timeline.Items[0].BodyRef != "ref-1" || len(body.Exchanges) != 1 || body.Exchanges[0].User != "body-1" || strings.Contains(response.Body.String(), "private local path") {
		t.Fatal(response.Body.String())
	}
}
func TestAPIV1SearchUsesGuardAndExistingHistorySearch(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/search?surface=codex&q=missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal(response.Code)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var body map[string]any
	json.Unmarshal(response.Body.Bytes(), &body)
	if _, ok := body["results"]; !ok {
		t.Fatal(body)
	}
}

type providerHistorySurface struct {
	*daemonSurface
	mu      sync.Mutex
	befores []int64
	fail    bool
	shared  *surface.TimelineItem
}

func (s *providerHistorySurface) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.befores = append(s.befores, request.Before)
	if request.Before == 0 {
		items := []surface.TimelineItem{{ID: "recent-1", Kind: "message", Role: "user", Text: "recent ask"}, {ID: "recent-2", Kind: "message", Role: "assistant", Text: "recent answer"}}
		if s.shared != nil {
			items = append(items, *s.shared)
		}
		return &surface.SessionReadResult{Items: items, NextBefore: 7, Source: "local-transcript"}, nil
	}
	if s.fail {
		return nil, errors.New("transcript unreadable")
	}
	items := []surface.TimelineItem{{ID: "older-1", Kind: "message", Role: "user", Text: "older ask"}}
	if s.shared != nil {
		items = append(items, *s.shared)
	}
	return &surface.SessionReadResult{Items: items, NextBefore: 0, Source: "local-transcript"}, nil
}

func TestProviderHistoryOmitsInjectedPromptAlreadyJournaled(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	shared := surface.TimelineItem{ID: "task-notification:task-1:completed", Kind: "event", Role: "system", Origin: "task-notification", Title: "Task notification", Text: "Synthetic job done", Status: "completed"}
	adapter := &providerHistorySurface{daemonSurface: fake, shared: &shared}
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	defer d.sources.shutdown()
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	read := func(query string) surface.SessionTimeline {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&limit=40"+query, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Timeline surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Timeline
	}
	first := read("")
	count := 0
	for _, item := range first.Items {
		if item.ID == shared.ID {
			count++
		}
	}
	older := read(fmt.Sprintf("&timelineBefore=%d", first.NextBefore))
	for _, item := range older.Items {
		if item.ID == shared.ID {
			count++
		}
	}
	if count != 1 || len(older.Items) != 1 || older.Items[0].ID != "older-1" {
		t.Fatalf("shared copies=%d first=%+v older=%+v", count, first.Items, older.Items)
	}
}

func TestSessionPageContinuesIntoProviderHistoryPastSeed(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &providerHistorySurface{daemonSurface: fake}
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	defer d.sources.shutdown()
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	read := func(query string) surface.SessionTimeline {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&limit=40"+query, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Timeline surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Timeline
	}
	first := read("")
	if first.Source != "journal" || len(first.Items) != 2 || first.NextBefore != encodeProviderHistoryCursor(7) {
		t.Fatalf("seeded page lost the provider boundary: %+v", first)
	}
	older := read(fmt.Sprintf("&timelineBefore=%d", first.NextBefore))
	if len(older.Items) != 1 || older.Items[0].Text != "older ask" || older.NextBefore != 0 || older.UnavailableReason != "" {
		t.Fatalf("older provider page=%+v", older)
	}
	adapter.mu.Lock()
	befores := append([]int64(nil), adapter.befores...)
	adapter.fail = true
	adapter.mu.Unlock()
	if len(befores) < 2 || befores[0] != 0 || befores[len(befores)-1] != 7 {
		t.Fatalf("provider reads=%v", befores)
	}
	failed := read(fmt.Sprintf("&timelineBefore=%d", first.NextBefore))
	if len(failed.Items) != 0 || failed.NextBefore != 0 || failed.UnavailableReason == "" {
		t.Fatalf("failed older read reported exhaustion: %+v", failed)
	}
}

func TestSessionPageWithPreexistingJournalReportsHistoryGapInsteadOfExhaustion(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	if _, _, err := registry.AppendSessionJournalEntry(registrypkg.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "legacy", Payload: []byte(`{"itemId":"legacy","version":1,"op":"upsert","kind":"text","role":"assistant","body":"legacy"}`)}, registrypkg.SessionJournalRetention{Count: 32, Bytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkSessionJournalSeed(from.ID, true); err != nil {
		t.Fatal(err)
	}
	adapter := &providerHistorySurface{daemonSurface: fake}
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	defer d.sources.shutdown()
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	get := func(query string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&limit=40"+query, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if err := d.sources.seed(context.Background(), &from, adapter); err != nil {
		t.Fatal(err)
	}
	response := get("")
	var body struct {
		Timeline surface.SessionTimeline `json:"timeline"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	if len(body.Timeline.Items) != 3 || body.Timeline.Items[0].Text != "legacy" || body.Timeline.NextBefore == 0 {
		t.Fatalf("pre-existing journal reported exhausted history: %+v", body.Timeline)
	}
	if _, provider := decodeProviderHistoryCursor(body.Timeline.NextBefore); provider {
		t.Fatalf("re-seed attached a provider boundary below older journal entries: %+v", body.Timeline)
	}
	older := get(fmt.Sprintf("&timelineBefore=%d", body.Timeline.NextBefore))
	if older.Code != http.StatusConflict || !strings.Contains(older.Body.String(), `"history_gap"`) {
		t.Fatalf("older read code=%d body=%s", older.Code, older.Body.String())
	}
}

func TestSessionPagesWalkOneExchangeAtATimeWithoutGapsOrOverlap(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	journal := []struct{ id, kind, role, turn, body string }{
		{"p1", "message", "user", "turn-1", "first question"},
		{"r1", "message", "assistant", "turn-1", "first answer"},
		{"r2", "text", "assistant", "turn-2", "unprompted answer"},
		{"p3", "message", "peer", "turn-3", "agent question"},
		{"t3", "toolCall", "", "turn-3", "lookup"},
		{"r3", "message", "assistant", "turn-3", "agent answer"},
	}
	if err := registry.RecordSessionJournalHistoryBoundary("from", 0); err != nil {
		t.Fatal(err)
	}
	for _, item := range journal {
		payload, _ := json.Marshal(sessionJournalPayload{ItemID: item.id, Kind: item.kind, Role: item.role, TurnID: item.turn, Body: item.body, Op: "upsert", Version: 1})
		if _, _, err := registry.AppendSessionJournalEntry(registrypkg.SessionJournalEntry{SessionID: "from", Kind: item.kind, ProviderKey: item.id, Payload: payload}, registrypkg.SessionJournalRetention{Count: 32, Bytes: 8192}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	var exchanges []surface.Exchange
	cursor := int64(0)
	for pages := 0; ; pages++ {
		if pages > len(journal) {
			t.Fatalf("paging did not end; seen=%v", seen)
		}
		target := "/api/v1/session?id=from&timeline=1&limit=1"
		if cursor > 0 {
			target += "&timelineBefore=" + strconv.FormatInt(cursor, 10)
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("page %d: %s", pages, response.Body.String())
		}
		var body struct {
			Exchanges []surface.Exchange      `json:"exchanges"`
			Timeline  surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Exchanges) != 1 {
			t.Fatalf("page %d exchanges=%+v", pages, body.Exchanges)
		}
		exchanges = append([]surface.Exchange{body.Exchanges[0]}, exchanges...)
		var ids []string
		for _, item := range body.Timeline.Items {
			ids = append(ids, item.ID)
		}
		seen = append(ids, seen...)
		if body.Timeline.NextBefore == 0 {
			break
		}
		cursor = body.Timeline.NextBefore
	}
	if strings.Join(seen, ",") != "p1,r1,r2,p3,t3,r3" {
		t.Fatalf("items across pages=%v", seen)
	}
	if len(exchanges) != 3 || exchanges[0].User != "first question" || exchanges[0].Assistant != "first answer" || exchanges[1].User != "" || exchanges[1].Assistant != "unprompted answer" || exchanges[2].User != "agent question" || exchanges[2].Assistant != "agent answer" {
		t.Fatalf("exchanges=%+v", exchanges)
	}
}

type toolHistorySurface struct {
	*daemonSurface
	reads []int64
}

func (s *toolHistorySurface) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.reads = append(s.reads, request.Before)
	page := request.Before
	if page == 0 {
		page = 1000
	}
	items := make([]surface.TimelineItem, 40)
	for index := range items {
		items[index] = surface.TimelineItem{ID: fmt.Sprintf("tool-%d-%d", page, index), Kind: "toolCall", Title: "Bash", Text: "true"}
	}
	return &surface.SessionReadResult{Items: items, NextBefore: page - 1, Source: "local-transcript"}, nil
}

func TestOlderProviderHistoryPageStopsAtTheItemLimit(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &toolHistorySurface{daemonSurface: fake}
	d := newTestDaemon(t, registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	read := func(cursor int64) surface.SessionTimeline {
		target := "/api/v1/session?id=from&timeline=1&limit=5"
		if cursor > 0 {
			target += "&timelineBefore=" + strconv.FormatInt(cursor, 10)
		}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Timeline surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Timeline
	}
	head := read(0)
	if len(head.Items) != 40 || head.NextBefore == 0 {
		t.Fatalf("head=%d items nextBefore=%d", len(head.Items), head.NextBefore)
	}
	older := read(head.NextBefore)
	if len(older.Items) != 400 || older.Items[0].ID != "tool-990-0" || older.Items[399].ID != "tool-999-39" {
		t.Fatalf("older page held %d items from %s", len(older.Items), older.Items[0].ID)
	}
	next := read(older.NextBefore)
	if len(next.Items) == 0 || next.Items[len(next.Items)-1].ID != "tool-989-39" {
		t.Fatalf("next page did not resume at the first unread provider page: %d items", len(next.Items))
	}
}
