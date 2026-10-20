package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func appendJournalPayloads(t *testing.T, r *registry.Registry, sessionID string, payloads ...sessionJournalPayload) {
	t.Helper()
	for _, payload := range payloads {
		if payload.Op == "" {
			payload.Op = "upsert"
		}
		payload.Version = 1
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: sessionID, Kind: payload.Kind, ProviderKey: payload.ItemID, Payload: encoded, ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPIV1SessionRejectsALimitOutsideTheExchangePage(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, limit := range []string{"0", "51", "abc", "-1"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id="+from.ID+"&limit="+limit, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_limit"`) || !strings.Contains(response.Body.String(), "between 1 and 50") {
			t.Fatalf("limit %s: status=%d body=%s", limit, response.Code, response.Body.String())
		}
	}
}

func TestReadJournalPageReturnsWholeExchangesPastTheItemsOfOneTurn(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	for turn := 1; turn <= 60; turn++ {
		id := fmt.Sprintf("t%02d", turn)
		at := time.Date(2026, 10, 19, 10, turn, 0, 0, time.UTC).Format(time.RFC3339Nano)
		appendJournalPayloads(t, r, from.ID, sessionJournalPayload{ItemID: id + "-prompt", Kind: "message", Role: "user", Body: "question " + id, TurnID: id, TS: at})
		tools := 1
		if turn == 60 {
			tools = 500
		}
		for call := range tools {
			appendJournalPayloads(t, r, from.ID, sessionJournalPayload{ItemID: fmt.Sprintf("%s-call-%d", id, call), Kind: "toolCall", Role: "assistant", Body: "ls", TurnID: id, TS: at})
		}
		appendJournalPayloads(t, r, from.ID,
			sessionJournalPayload{ItemID: id + "-answer", Kind: "message", Role: "assistant", Body: "answer " + id, TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-done", Kind: "done", TurnID: id, TS: at},
		)
	}
	page, err := d.readJournalPage(from.ID, 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Exchanges) != 6 || page.NextBefore == 0 {
		t.Fatalf("exchanges=%d nextBefore=%d", len(page.Exchanges), page.NextBefore)
	}
	for index, exchange := range page.Exchanges {
		id := fmt.Sprintf("t%02d", 55+index)
		if exchange.User != "question "+id || exchange.Assistant != "answer "+id || exchange.Timestamp.IsZero() {
			t.Fatalf("exchange %d = %+v", index, exchange)
		}
	}
	older, err := d.readJournalPage(from.ID, uint64(page.NextBefore), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Exchanges) != 50 || older.Exchanges[49].User != "question t54" || older.Exchanges[0].User != "question t05" {
		t.Fatalf("older page has %d exchanges: first=%+v", len(older.Exchanges), older.Exchanges[0])
	}
}

type rederiveSurface struct {
	*daemonSurface
	items []surface.TimelineItem
	calls int
	from  int64
}

func (s *rederiveSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{Items: s.items[len(s.items)-2:], TranscriptOffsetSet: true, TranscriptOffset: 900, TranscriptIdentity: "transcript"}, nil
}

func (s *rederiveSurface) TranscriptItems(_ context.Context, _ *surface.Session, from int64) ([]surface.TimelineItem, error) {
	s.calls++
	s.from = from
	return s.items, nil
}

// earlierDerivationJournal holds what an earlier derivation wrote: rows
// without turns, a prompt overwritten by a later notification of its task,
// and a source error between the turns.
func earlierDerivationJournal(t *testing.T, r *registry.Registry, sessionID string) {
	t.Helper()
	appendTimeline := func(payload sessionJournalPayload) {
		payload.Op, payload.Version = "upsert", 1
		key := "timeline:" + payload.ItemID
		if payload.Kind == "done" {
			payload.Op = "phase"
			payload.ItemID += ":phase:done"
			key += ":phase:done"
		}
		payload.ProviderKey = key
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: sessionID, Kind: payload.Kind, ProviderKey: key, Payload: encoded}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	appendTimeline(sessionJournalPayload{ItemID: "100-aaaaaa-0", Kind: "message", Role: "user", Body: "first", TS: "2026-10-19T10:00:00Z"})
	appendTimeline(sessionJournalPayload{ItemID: "200-bbbbbb-0", Kind: "message", Role: "assistant", Body: "one", TS: "2026-10-19T10:00:01Z"})
	encoded, _ := json.Marshal(sessionJournalPayload{ItemID: "source-error", Op: "reset", Kind: "source-error", Reason: "transient"})
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: sessionID, Kind: "source-error", Payload: encoded}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	appendTimeline(sessionJournalPayload{ItemID: "400-dddddd-0", Kind: "message", Role: "assistant", Body: "two", TS: "2026-10-19T10:01:01Z"})
	appendTimeline(sessionJournalPayload{ItemID: "task-notification:monitor:", Kind: "event", Role: "system", Origin: "task-notification", Body: "third event", TS: "2026-10-19T10:02:00Z"})
	appendTimeline(sessionJournalPayload{ItemID: "600-ffffff-0", Kind: "message", Role: "assistant", Body: "three", TS: "2026-10-19T10:02:01Z"})
	if err := r.MarkSessionJournalSeedWithIdentity(sessionID, true, "transcript"); err != nil {
		t.Fatal(err)
	}
}

func transcriptItems() []surface.TimelineItem {
	return []surface.TimelineItem{
		{ID: "100-aaaaaa-0", Kind: "message", Role: "user", Text: "first", TurnID: "u1", Timestamp: "2026-10-19T10:00:00Z"},
		{ID: "200-bbbbbb-0", Kind: "message", Role: "assistant", Text: "one", TurnID: "u1", Timestamp: "2026-10-19T10:00:01Z"},
		{ID: "200-bbbbbb-1", Kind: "done", TurnID: "u1", Timestamp: "2026-10-19T10:00:01Z"},
		{ID: "task-notification:monitor::aaaa", Kind: "event", Role: "system", Origin: "task-notification", Text: "second event", TurnID: "u2", Timestamp: "2026-10-19T10:01:00Z"},
		{ID: "400-dddddd-0", Kind: "message", Role: "assistant", Text: "two", TurnID: "u2", Timestamp: "2026-10-19T10:01:01Z"},
		{ID: "task-notification:monitor::bbbb", Kind: "event", Role: "system", Origin: "task-notification", Text: "third event", TurnID: "u3", Timestamp: "2026-10-19T10:02:00Z"},
		{ID: "600-ffffff-0", Kind: "message", Role: "assistant", Text: "three", TurnID: "u3", Timestamp: "2026-10-19T10:02:01Z"},
		{ID: "700-gggggg-0", Kind: "message", Role: "assistant", Text: "no turn markers"},
	}
}

func TestSourceStartRederivesAnEarlierJournalOnce(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	from.Surface, from.RuntimeKind, from.Transport, from.Transcript = surface.KindClaude, surface.RuntimeClaudeCLI, "", "/transcript.jsonl"
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	earlierDerivationJournal(t, r, from.ID)
	adapter := &rederiveSurface{daemonSurface: fake, items: transcriptItems()}
	start := func() {
		source := &sessionSource{manager: newSessionSourceManager(r), session: from, adapter: adapter, epoch: "rederive", ctx: context.Background(), appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
		if err := source.loadSeed(); err != nil {
			t.Fatal(err)
		}
	}
	start()
	first, err := r.ReadSessionJournalPage(from.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	start()
	second, err := r.ReadSessionJournalPage(from.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 || adapter.from != 100 || second.LatestSeq != first.LatestSeq {
		t.Fatalf("rederived %d times from %d; latest %d then %d", adapter.calls, adapter.from, first.LatestSeq, second.LatestSeq)
	}
	var order []string
	for _, entry := range first.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		order = append(order, payload.ItemID+"@"+payload.TurnID)
	}
	want := []string{"100-aaaaaa-0@u1", "200-bbbbbb-0@u1", "source-error@", "200-bbbbbb-1:phase:done@u1", "task-notification:monitor::aaaa@u2", "400-dddddd-0@u2", "task-notification:monitor::bbbb@u3", "600-ffffff-0@u3", "700-gggggg-0@"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("journal order\n got %v\nwant %v", order, want)
	}
	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for index, exchange := range page.Exchanges[:3] {
		if exchange.User == "" || exchange.Timestamp.IsZero() {
			t.Fatalf("exchange %d = %+v", index, exchange)
		}
	}
}

func TestSessionStreamCursorRecoversAcrossARederivedJournal(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	from.Surface, from.RuntimeKind, from.Transport, from.Transcript = surface.KindClaude, surface.RuntimeClaudeCLI, "", "/transcript.jsonl"
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	earlierDerivationJournal(t, r, from.ID)
	adapter := &rederiveSurface{daemonSurface: fake, items: transcriptItems()}
	source := &sessionSource{manager: newSessionSourceManager(r), session: from, adapter: adapter, epoch: "rederive", ctx: context.Background(), appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	if err := source.catchUpLocalTranscript(context.Background(), "transcript"); err != nil {
		t.Fatal(err)
	}
	before, err := r.ReadSessionJournalPage(from.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.rederiveJournal(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.kind, fake.caps.Stream = surface.KindClaude, true
	d.Surfaces = []surface.Surface{adapter}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	stream := func(after uint64) *httptest.ResponseRecorder {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/session-stream?id=%s&after=%d", from.ID, after), nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	current := stream(before.LatestSeq)
	for _, id := range []string{"100-aaaaaa-0", "task-notification:monitor::aaaa", "600-ffffff-0"} {
		if !strings.Contains(current.Body.String(), `"itemId":"`+id+`"`) {
			t.Fatalf("a client at the newest cursor did not receive %s: %s", id, current.Body.String())
		}
	}
	inside := stream(before.Entries[1].Seq)
	if inside.Code != http.StatusConflict || !strings.Contains(inside.Body.String(), `"stream_gap"`) {
		t.Fatalf("a cursor inside the rebuilt span: status=%d body=%s", inside.Code, inside.Body.String())
	}
}

func TestReadJournalPageStopsAtWholeExchangesThatFitAPage(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	body := strings.Repeat("x", 12<<10)
	for turn := 1; turn <= 30; turn++ {
		id := fmt.Sprintf("t%02d", turn)
		at := time.Date(2026, 10, 19, 10, turn, 0, 0, time.UTC).Format(time.RFC3339Nano)
		appendJournalPayloads(t, r, from.ID, sessionJournalPayload{ItemID: id + "-prompt", Kind: "message", Role: "user", Body: "question " + id, TurnID: id, TS: at})
		for call := range 4 {
			appendJournalPayloads(t, r, from.ID, sessionJournalPayload{ItemID: fmt.Sprintf("%s-out-%d", id, call), Kind: "toolResult", Role: "user", Body: body, TurnID: id, TS: at})
		}
		appendJournalPayloads(t, r, from.ID, sessionJournalPayload{ItemID: id + "-answer", Kind: "message", Role: "assistant", Body: "answer " + id, TurnID: id, TS: at})
	}
	var users []string
	cursor := uint64(0)
	for range 10 {
		page, err := d.readJournalPage(from.ID, cursor, 50)
		var gap *registry.SessionJournalHistoryGapError
		if errors.As(err, &gap) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		size := 0
		for _, item := range page.Items {
			size += len(item.Text)
		}
		if size > sessionPageBytes || page.Truncated || len(page.Exchanges) == 0 || page.Exchanges[0].User == "" {
			t.Fatalf("page of %d bytes, truncated=%v, exchanges=%d", size, page.Truncated, len(page.Exchanges))
		}
		var pageUsers []string
		for _, exchange := range page.Exchanges {
			pageUsers = append(pageUsers, exchange.User)
		}
		users = append(pageUsers, users...)
		if page.NextBefore <= 0 {
			break
		}
		cursor = uint64(page.NextBefore)
	}
	if len(users) != 30 || users[0] != "question t01" || users[29] != "question t30" {
		t.Fatalf("paged %d exchanges: %v", len(users), users)
	}
}
