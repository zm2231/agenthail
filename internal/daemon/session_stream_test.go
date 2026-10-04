package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	d = New(reg, []surface.Surface{adapter})
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

func TestAPISessionStreamReturnsTypedGap(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
	d = New(reg, []surface.Surface{adapter})
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
