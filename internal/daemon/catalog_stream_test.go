package daemon

import (
	"bufio"
	"context"
	"encoding/json"
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

func TestDiscoveryStreamsFullDashboardSessionRow(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	window, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range window.Events {
		if event.Type != "session.upserted" {
			continue
		}
		var payload struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Session.ID == "" || payload.Session.Capabilities != d.Surfaces[0].Capabilities() || payload.Session.ObservedAt.IsZero() || payload.Session.HostProject == nil || payload.Session.Checkout == nil {
			t.Fatalf("payload=%+v", payload.Session)
		}
		return
	}
	t.Fatal("session.upserted event was not recorded")
}

func TestDiscoveryStreamsProjectionChangesIncludingReturnToPriorValue(t *testing.T) {
	d, registry, _, from, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	initial, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAlias("reviewer", from.ID); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	changed, err := registry.CatalogEventsAfter(initial.LatestSeq, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Events) != 1 || changed.Events[0].Type != "session.upserted" {
		t.Fatalf("changed=%+v", changed.Events)
	}
	var row struct {
		Session dashboardSession `json:"session"`
	}
	if err := json.Unmarshal(changed.Events[0].Payload, &row); err != nil || row.Session.Alias != "reviewer" {
		t.Fatalf("row=%+v err=%v", row.Session, err)
	}
	if err := registry.RemoveAlias("reviewer"); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	returned, err := registry.CatalogEventsAfter(changed.LatestSeq, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(returned.Events) != 1 || returned.Events[0].Type != "session.upserted" {
		t.Fatalf("returned=%+v", returned.Events)
	}
	row = struct {
		Session dashboardSession `json:"session"`
	}{}
	if err := json.Unmarshal(returned.Events[0].Payload, &row); err != nil || row.Session.Alias != "" {
		t.Fatalf("row=%+v err=%v", row.Session, err)
	}
}
