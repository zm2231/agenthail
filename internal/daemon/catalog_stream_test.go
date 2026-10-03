package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPICatalogStreamReplaysPersistedEvent(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	if _, created, err := d.catalog.publish(registry.CatalogEvent{DedupeKey: "session:from:1", Type: "session.upserted", EntityID: "from", Payload: []byte(`{"id":"from"}`)}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/api/v1/catalog-events?after=0", nil)
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
		t.Fatalf("status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"stream":"catalog"`) || !strings.Contains(line, `"type":"session.upserted"`) {
				t.Fatalf("line=%q", line)
			}
			break
		}
	}
}

func TestDiscoveryPersistsCatalogBeforeSnapshotReads(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.listCalls.Store(0)
	d.discoverCatalog(context.Background())
	if fake.listCalls.Load() != 1 {
		t.Fatalf("discovery calls=%d", fake.listCalls.Load())
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.HostEpoch == "" || state.CatalogSeq == 0 || len(state.Sessions) != 2 || fake.listCalls.Load() != 1 {
		t.Fatalf("state=%+v providerCalls=%d", state, fake.listCalls.Load())
	}
	for _, session := range state.Sessions {
		if session.ObservedAt.IsZero() || session.UnavailableReason == "" {
			t.Fatalf("session=%+v", session)
		}
	}
	if len(state.Surfaces) != 1 || state.Surfaces[0].Health != "healthy" || !state.Surfaces[0].Connected {
		t.Fatalf("surfaces=%+v", state.Surfaces)
	}
}

func TestDiscoveryRemovesOnlyAfterTwoSuccessfulOmissions(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	fake.sessions = map[string]surface.Session{}
	d.discoverCatalog(context.Background())
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 2 {
		t.Fatalf("first omission sessions=%d", len(state.Sessions))
	}
	d.discoverCatalog(context.Background())
	state, err = d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 0 {
		t.Fatalf("second omission sessions=%d", len(state.Sessions))
	}
	window, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	removals := 0
	for _, event := range window.Events {
		if event.Type == "session.removed" {
			removals++
		}
	}
	if removals != 2 {
		t.Fatalf("removals=%d", removals)
	}
}
