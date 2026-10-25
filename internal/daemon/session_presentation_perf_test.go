package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestMeasureSessionPresentation(t *testing.T) {
	if os.Getenv("AGENTHAIL_MEASURE_PRESENTATION") != "1" {
		t.Skip("opt-in scratch HTTP measurement")
	}
	d, store, fake, _, _ := daemonFixture(t)
	now := time.Now().UTC()
	states := make([]registry.CatalogSessionState, 0, 572)
	for i := 0; i < 4774; i++ {
		id := fmt.Sprintf("fixture-%04d", i)
		last := now.Add(-time.Duration(48+i%500) * time.Hour)
		session := surface.Session{ID: id, Name: fmt.Sprintf("Synthetic work %04d", i), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: last, Cwd: fmt.Sprintf("/synthetic/project-%02d", i%12)}
		if i < 20 {
			session.LastActive = now.Add(-time.Duration(i) * time.Minute)
		}
		if i < 8 {
			session.Status = surface.StatusBusy
		}
		if i >= 469 && i < 572 {
			session.Subagent = &surface.Subagent{ParentID: fmt.Sprintf("fixture-%04d", i-469), RootID: fmt.Sprintf("fixture-%04d", i-469), Depth: 1}
			session.LastActive = now.Add(-time.Hour)
		}
		if i >= 8 && i < 12 {
			session.WaitingOn = &surface.Waiting{ID: id, Kind: surface.WaitingQuestion}
		}
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		if i >= 572 {
			continue
		}
		project := json.RawMessage(fmt.Sprintf(`{"id":"project-%02d","localId":"project-%02d","displayName":"Project %02d"}`, i%12, i%12, i%12))
		row := dashboardSession{ID: id, Name: session.Name, Surface: session.Surface, RuntimeKind: session.RuntimeKind, Status: session.Status, LastActive: session.LastActive, Cwd: session.Cwd, Subagent: session.Subagent, WaitingOn: session.WaitingOn, Current: i < 20 || (i >= 469 && i < 572), CurrentReason: "recent"}
		if err := json.Unmarshal(project, &row.Project); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(row)
		state := registry.CatalogSessionState{Session: session, Project: project, Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: string(raw)}
		states = append(states, state)
		if _, _, err := store.RecordCatalogSession(state, registry.CatalogEvent{DedupeKey: id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MarkCatalogDiscoveryFailure(surface.KindCodex, "synthetic stale discovery", now); err != nil {
		t.Fatal(err)
	}
	for i, state := range states[:12] {
		if _, _, err := store.RecordCatalogSession(state, registry.CatalogEvent{DedupeKey: fmt.Sprintf("fresh-%d", i), Type: "session.upserted", EntityID: state.Session.ID, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 12; i < 20; i++ {
		if err := store.QueueMessage(states[i].Session.ID, "synthetic queued message"); err != nil {
			t.Fatal(err)
		}
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	server := httptest.NewServer(handler)
	defer server.Close()
	t.Log("fixture: registry=4776 catalog=572 roots=469 children=103 stale=560 queued=8 busy=8 waiting=4; synthetic only; hosts=0 pairing=0 devices=0")
	for _, path := range []string{"/api/v1/snapshot", "/api/v1/snapshot?fresh=1", "/api/v1/snapshot?scope=current&limit=200", "/api/v1/snapshot?scope=all&limit=200"} {
		elapsed := make([]float64, 0, 101)
		size := 0
		var first float64
		for i := 0; i < 102; i++ {
			req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			start := time.Now()
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("%s: status=%d err=%v body=%s", path, response.StatusCode, err, body)
			}
			ms := float64(time.Since(start)) / float64(time.Millisecond)
			size = len(body)
			if i == 0 {
				first = ms
			} else {
				elapsed = append(elapsed, ms)
			}
		}
		sort.Float64s(elapsed)
		t.Logf("%s first_ms=%.3f median_ms=%.3f p95_ms=%.3f bytes=%d n=%d", path, first, elapsed[len(elapsed)/2], elapsed[95], size, len(elapsed))
	}
	if got := fake.listCalls.Load(); got != 0 {
		t.Fatalf("list provider calls=%d", got)
	}
}
