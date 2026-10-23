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
		source := &sessionSource{manager: newTestSourceManager(t, r), session: from, adapter: adapter, epoch: "rederive", ctx: context.Background(), appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
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
	source := &sessionSource{manager: newTestSourceManager(t, r), session: from, adapter: adapter, epoch: "rederive", ctx: context.Background(), appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
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

func TestReadJournalPageCountsCodexTurnsNotTheirMessages(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	for turn := 1; turn <= 8; turn++ {
		id := fmt.Sprintf("t%d", turn)
		at := time.Date(2026, 10, 21, 10, turn, 0, 0, time.UTC).Format(time.RFC3339Nano)
		appendJournalPayloads(t, r, from.ID,
			sessionJournalPayload{ItemID: id + "-permissions", Kind: "instructions", Title: "Instructions", Body: "<permissions instructions>sandboxed</permissions instructions>", TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-agents", Kind: "instructions", Title: "Instructions", Body: "# AGENTS.md instructions for /work\n<INSTRUCTIONS>Be brief.</INSTRUCTIONS>", TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-prompt", Kind: "message", Role: "user", Body: "prompt " + id, TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-recall", Kind: "instructions", Title: "Instructions", Body: "<canon-recall>recalled</canon-recall>", TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-commentary", Kind: "message", Role: "assistant", Body: "checking " + id, TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-answer", Kind: "message", Role: "assistant", Body: "answer " + id, TurnID: id, TS: at},
			sessionJournalPayload{ItemID: id + "-done", Kind: "done", TurnID: id, TS: at},
		)
	}
	page, err := d.readJournalPage(from.ID, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	older, err := d.readJournalPage(from.ID, uint64(page.NextBefore), 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		page  *surface.SessionReadResult
		turns []int
	}{{page, []int{6, 7, 8}}, {older, []int{3, 4, 5}}} {
		if len(check.page.Exchanges) != len(check.turns) {
			t.Fatalf("exchanges=%+v, want turns %v", check.page.Exchanges, check.turns)
		}
		for index, turn := range check.turns {
			exchange := check.page.Exchanges[index]
			if exchange.User != fmt.Sprintf("prompt t%d", turn) || exchange.Assistant != fmt.Sprintf("answer t%d", turn) {
				t.Fatalf("exchange %d = %+v", index, exchange)
			}
		}
	}
}

func TestJournaledCodexContextFromBeforeInstructionsReadsAsInstructions(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	at := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	agents := "# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\n" + strings.Repeat("Read the code before editing.\n", 1200) + "</INSTRUCTIONS>"
	environment := "<environment_context>\n  <cwd>/work</cwd>\n</environment_context>"
	long, err := sessionJournalEntry(from.ID, sessionJournalPayload{ItemID: "t1-agents", Op: "upsert", Version: 1, Kind: "message", Role: "user", Body: agents + "\n\n" + environment, TurnID: "t1", TS: at})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.AppendSessionJournalEntry(long, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	appendJournalPayloads(t, r, from.ID,
		sessionJournalPayload{ItemID: "t1-environment", Kind: "message", Role: "user", Body: environment, TurnID: "t1", TS: at},
		sessionJournalPayload{ItemID: "t1-unretained", Kind: "message", Role: "user", Body: "# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\nBe", Truncated: true, TruncationReason: "full_body_not_retained", TurnID: "t1", TS: at},
		sessionJournalPayload{ItemID: "t1-prompt", Kind: "message", Role: "user", Body: "Fix the build.", TurnID: "t1", TS: at},
		sessionJournalPayload{ItemID: "t1-answer", Kind: "message", Role: "assistant", Body: "Fixed.", TurnID: "t1", TS: at},
		sessionJournalPayload{ItemID: "t1-done", Kind: "done", TurnID: "t1", TS: at},
	)
	page, err := d.readJournalPage(from.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, item := range page.Items {
		kinds[item.ID] = item.Kind + "/" + item.Role
	}
	for id, want := range map[string]string{"t1-agents": "instructions/", "t1-environment": "instructions/", "t1-unretained": "message/user", "t1-prompt": "message/user"} {
		if kinds[id] != want {
			t.Fatalf("%s read as %q, want %q (items %v)", id, kinds[id], want, kinds)
		}
	}
	if len(page.Exchanges) != 1 || !strings.HasSuffix(page.Exchanges[0].User, "Fix the build.") || strings.Contains(page.Exchanges[0].User, "environment_context") || page.Exchanges[0].Assistant != "Fixed." {
		t.Fatalf("exchanges=%+v", page.Exchanges)
	}
}

func TestCodexJournalContextPastAMebibyteIsReadWholeAndOnce(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	huge := "# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\n" + strings.Repeat("Keep the tests hermetic and the names neutral.\n", 32<<10) + "</INSTRUCTIONS>"
	if len(huge) <= 1<<20 {
		t.Fatalf("fixture is %d bytes, want more than a mebibyte", len(huge))
	}
	entry, err := sessionJournalEntry(from.ID, sessionJournalPayload{ItemID: "t1-agents", Op: "upsert", Version: 1, Kind: "message", Role: "user", Body: huge, TurnID: "t1"})
	if err != nil || entry.BodyRef == "" {
		t.Fatalf("entry ref=%q err=%v", entry.BodyRef, err)
	}
	if _, _, err := r.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	appendJournalPayloads(t, r, from.ID,
		sessionJournalPayload{ItemID: "t1-prompt", Kind: "message", Role: "user", Body: "Fix the build.", TurnID: "t1"},
		sessionJournalPayload{ItemID: "t1-done", Kind: "done", TurnID: "t1"},
	)
	page, err := d.readJournalPage(from.ID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 || page.Items[0].ID != "t1-agents" || page.Items[0].Kind != "instructions" {
		t.Fatalf("items=%+v", page.Items)
	}

	reads := 0
	read := codexJournalItemReader(func(ref string) ([]byte, error) {
		reads++
		return []byte(huge), nil
	})
	var payload sessionJournalPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if item := read(payload); item.Kind != "instructions" {
			t.Fatalf("item=%+v", item)
		}
	}
	if reads != 1 {
		t.Fatalf("retained body read %d times for one row", reads)
	}
}

func TestJournalPageKeepsTheInstructionsOfItsOldestTurn(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	for turn := 1; turn <= 5; turn++ {
		id := fmt.Sprintf("t%d", turn)
		appendJournalPayloads(t, r, from.ID,
			sessionJournalPayload{ItemID: id + "-instructions", Kind: "instructions", Title: "Instructions", Body: "<environment_context>turn " + id + "</environment_context>", TurnID: id},
			sessionJournalPayload{ItemID: id + "-prompt", Kind: "message", Role: "user", Body: "prompt " + id, TurnID: id},
			sessionJournalPayload{ItemID: id + "-answer", Kind: "message", Role: "assistant", Body: "answer " + id, TurnID: id},
			sessionJournalPayload{ItemID: id + "-done", Kind: "done", TurnID: id},
		)
	}
	seen := map[string]bool{}
	cursor := uint64(0)
	for _, want := range [][]int{{4, 5}, {2, 3}, {1}} {
		page, err := d.readJournalPage(from.ID, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("item %s returned on two pages", item.ID)
			}
			seen[item.ID], ids[item.ID] = true, true
		}
		for _, turn := range want {
			for _, part := range []string{"instructions", "prompt", "answer", "done"} {
				if id := fmt.Sprintf("t%d-%s", turn, part); !ids[id] {
					t.Fatalf("page for turns %v lacks %s: %v", want, id, ids)
				}
			}
		}
		cursor = uint64(page.NextBefore)
	}
	if len(seen) != 20 {
		t.Fatalf("pages returned %d of 20 items", len(seen))
	}
}

func TestJournalPagesNeverSplitATurnWhoseIDIsReused(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	appendJournalPayloads(t, r, from.ID,
		sessionJournalPayload{ItemID: "first", Kind: "message", Role: "user", Body: "first", TurnID: "t1"},
		sessionJournalPayload{ItemID: "one", Kind: "message", Role: "assistant", Body: "one", TurnID: "t1"},
		sessionJournalPayload{ItemID: "done", Kind: "done", TurnID: "t1"},
		sessionJournalPayload{ItemID: "after", Kind: "message", Role: "peer", Origin: "peer", Sender: "reviewer", Body: "after the close", TurnID: "t1"},
		sessionJournalPayload{ItemID: "two", Kind: "message", Role: "assistant", Body: "two", TurnID: "t1"},
	)
	newest, err := d.readJournalPage(from.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	older, err := d.readJournalPage(from.ID, uint64(newest.NextBefore), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(newest.Exchanges) != 1 || newest.Exchanges[0].Assistant != "two" || len(newest.Items) != 2 {
		t.Fatalf("newest page exchanges=%+v items=%+v", newest.Exchanges, newest.Items)
	}
	if len(older.Exchanges) != 1 || older.Exchanges[0].Assistant != "one" || len(older.Items) != 3 {
		t.Fatalf("older page exchanges=%+v items=%+v", older.Exchanges, older.Items)
	}
}
