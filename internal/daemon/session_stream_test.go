package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPISessionStreamReplaysJournalWithoutNewProviderRead(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
	d = newTestDaemon(t, reg, []surface.Surface{adapter})
	manager := newSessionSourceManager(reg)
	d.sources = manager
	first, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	adapter.events <- surface.StreamEvent{ID: "m1", ProviderKey: "m1", Version: 1, Operation: "append", Kind: "text", Text: "ready"}
	select {
	case <-first.Entries:
	case <-time.After(time.Second):
		t.Fatal("source did not journal event")
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("initial provider stream calls=%d", calls)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/api/v1/session-stream?id="+from.ID+"&after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"stream":"session"`) || !strings.Contains(line, `"body":"ready"`) {
				t.Fatalf("line=%q", line)
			}
			break
		}
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("replay caused provider stream calls=%d", calls)
	}
}

func TestAPISessionStreamRejectsClaudeWithoutLiveStream(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindClaude
	from.RuntimeKind = surface.RuntimeClaudeCLI
	from.Transport = "uds"
	fake.kind = surface.KindClaude
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "seed-api", Kind: "text", Text: "seeded over API"}},
	}
	adapter.caps.Stream = false
	d = newTestDaemon(t, reg, []surface.Surface{adapter})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/api/v1/session-stream?id="+from.ID+"&after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"code":"stream_unsupported"`) {
		t.Fatalf("body=%s", body)
	}
}

func TestAPISessionStreamReplaysSameProviderKeyMutationAfterCursor(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent)}
	adapter.caps.Stream = true
	d = newTestDaemon(t, reg, []surface.Surface{adapter})
	first, inserted, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "message-1", Payload: []byte(`{"itemId":"message-1","version":1,"op":"upsert","kind":"text","ts":"2026-10-03T12:00:00Z","body":"first"}`)}, registry.SessionJournalRetention{Count: 2, Bytes: 1024})
	if err != nil || !inserted {
		t.Fatalf("first=%+v inserted=%v err=%v", first, inserted, err)
	}
	updated, inserted, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "message-1", Payload: []byte(`{"itemId":"message-1","version":2,"op":"upsert","kind":"text","ts":"2026-10-03T12:00:01Z","body":"latest"}`)}, registry.SessionJournalRetention{Count: 2, Bytes: 1024})
	if err != nil || !inserted || updated.Seq <= first.Seq {
		t.Fatalf("updated=%+v inserted=%v err=%v", updated, inserted, err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session-stream?id="+from.ID+"&after="+strconv.FormatUint(first.Seq, 10), nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"seq":`+strconv.FormatUint(updated.Seq, 10)) || !strings.Contains(line, `"version":2`) || !strings.Contains(line, `"body":"latest"`) {
				t.Fatalf("line=%q", line)
			}
			return
		}
	}
}

func TestAPISessionStreamReturnsTypedGap(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
	d = newTestDaemon(t, reg, []surface.Surface{adapter})
	for index := 0; index < 4; index++ {
		if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: string(rune('a' + index)), Payload: []byte(`{"itemId":"item","version":1}`)}, registry.SessionJournalRetention{Count: 2, Bytes: 1024}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session-stream?id="+from.ID+"&after=1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

func TestAPISessionStreamBodyRequiresReadAuthorizationAndBoundsRanges(t *testing.T) {
	d, reg, _, from, _ := daemonFixture(t)
	body := strings.Repeat("x", sessionStreamBodyBytes+1)
	if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "body", Payload: []byte(`{"itemId":"body","version":1,"bodyRef":"opaque"}`), BodyRef: "opaque", FullBody: []byte(body)}, registry.SessionJournalRetention{Count: 2, Bytes: sessionJournalRetentionBytes}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	unauthorized, err := http.Get(server.URL + "/api/v1/session-stream-body?id=" + from.ID + "&ref=opaque&start=0")
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.StatusCode)
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session-stream-body?id="+from.ID+"&ref=opaque&start=0&end="+strconv.Itoa(sessionStreamBodyBytes+1), nil)
	request.Header.Set("Authorization", "Bearer secret")
	tooLarge, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer tooLarge.Body.Close()
	if tooLarge.StatusCode != http.StatusBadRequest {
		t.Fatalf("range status=%d", tooLarge.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/session-stream-body?id="+from.ID+"&ref=opaque&start=2&end=8", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		Body      string `json:"body"`
		Start     int    `json:"start"`
		End       int    `json:"end"`
		Total     int    `json:"total"`
		Truncated bool   `json:"truncated"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&payload) != nil || payload.Body != "xxxxxx" || payload.Start != 2 || payload.End != 8 || payload.Total != len(body) || !payload.Truncated {
		t.Fatalf("status=%d payload=%+v", response.StatusCode, payload)
	}
}

func TestASessionStreamCarriesEachRowAsTheSessionPageReadsIt(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent)}
	adapter.caps.Stream = true
	d = newTestDaemon(t, reg, []surface.Surface{adapter})
	rows := []string{
		`{"itemId":"context-1","version":1,"op":"upsert","kind":"message","role":"user","ts":"2026-10-03T12:00:00Z","body":"<environment_context>cwd</environment_context>"}`,
		`{"itemId":"prompt-1","version":1,"op":"upsert","kind":"message","role":"user","ts":"2026-10-03T12:00:01Z","body":"hello"}`,
		`{"itemId":"reply-1","version":1,"op":"upsert","kind":"text","ts":"2026-10-03T12:00:02Z","body":"hi"}`,
		`{"itemId":"gone","version":1,"op":"remove","kind":"text","ts":"2026-10-03T12:00:03Z"}`,
		`{"itemId":"e:source","version":1,"op":"reset","kind":"source-error","ts":"2026-10-03T12:00:04Z","reason":"down"}`,
	}
	for index, row := range rows {
		if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: strconv.Itoa(index), Payload: []byte(row)}, registry.SessionJournalRetention{Count: 50, Bytes: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	paged := map[string]surface.TimelineItem{}
	for _, item := range page.Items {
		paged[item.ID] = item
	}
	if paged["context-1"].Kind != "instructions" {
		t.Fatalf("the page reads injected context as instructions: %+v", paged["context-1"])
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/session-stream?id="+from.ID+"&after=0", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	for seen := 0; seen < len(rows); {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		seen++
		var envelope struct {
			Data struct {
				ItemID string `json:"itemId"`
			} `json:"data"`
			Item *surface.TimelineItem `json:"item"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope); err != nil {
			t.Fatal(err)
		}
		want, onPage := paged[envelope.Data.ItemID]
		switch {
		case !onPage && envelope.Item != nil:
			t.Fatalf("a row the page leaves out carries an item: %s %+v", envelope.Data.ItemID, envelope.Item)
		case onPage && (envelope.Item == nil || !reflect.DeepEqual(*envelope.Item, want)):
			t.Fatalf("row %s streams as %+v, the page reads %+v", envelope.Data.ItemID, envelope.Item, want)
		}
	}
}
