package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionViewRowsOmitObservedAtAndADiscoveryRefreshKeepsTheirKey(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	session := surface.Session{ID: "seen", Name: "Seen", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	raw, _ := json.Marshal(dashboardSession{ID: session.ID, Name: session.Name, Surface: session.Surface, Status: session.Status})
	if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: session.ID, Type: "session.upserted", EntityID: session.ID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	d.sessionViewHandler(w, httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?scope=all&members=1", nil))
	var body struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Sessions) != 1 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	row := body.Sessions[0]
	if _, ok := row["observedAt"]; ok {
		t.Fatalf("a view row carries observedAt: %s", w.Body.String())
	}
	var freshness map[string]json.RawMessage
	if json.Unmarshal(row["freshness"], &freshness) != nil || len(freshness) != 2 || freshness["generation"] == nil || freshness["stale"] == nil {
		t.Fatalf("freshness=%s", row["freshness"])
	}
	before, err := store.ReadCatalogPresentationKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{session.ID: {}}, now.Add(time.Minute), true, 3); err != nil {
		t.Fatal(err)
	}
	after, err := store.ReadCatalogPresentationKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("a discovery pass that only refreshed observedAt moved the view key %+v -> %+v", before, after)
	}
	snapshot := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	d.dashboardHandler(&dashboardServer{token: dashboardTestToken}).ServeHTTP(snapshot, request)
	if !strings.Contains(snapshot.Body.String(), `"observedAt"`) {
		t.Fatalf("the unscoped snapshot lost observedAt: %d %s", snapshot.Code, snapshot.Body.String())
	}
}
