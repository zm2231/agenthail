package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestStreamsSendBodyBytesBeforeTheFirstKeepalive(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
	d = New(reg, []surface.Surface{adapter})
	entry, inserted, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "message-1", Payload: []byte(`{"itemId":"message-1","version":1,"op":"upsert","kind":"text","ts":"2026-10-03T12:00:00Z","body":"first"}`)}, registry.SessionJournalRetention{Count: 2, Bytes: 1024})
	if err != nil || !inserted {
		t.Fatalf("entry=%+v inserted=%v err=%v", entry, inserted, err)
	}
	catalog, err := reg.CatalogEventsAfter(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	for name, path := range map[string]string{
		"events":  "/api/v1/events",
		"catalog": "/api/v1/catalog-events?after=" + strconv.FormatUint(catalog.LatestSeq, 10),
		"session": "/api/v1/session-stream?id=" + from.ID + "&after=" + strconv.FormatUint(entry.Seq, 10),
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			cancel()
			t.Fatalf("%s: %v", name, err)
		}
		line, err := bufio.NewReader(response.Body).ReadString('\n')
		response.Body.Close()
		cancel()
		if err != nil || line != ": connected\n" {
			t.Fatalf("%s stream with nothing to replay sent %q before its first keepalive: %v", name, line, err)
		}
	}
}
