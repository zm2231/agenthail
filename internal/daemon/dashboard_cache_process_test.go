package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestParseClaudeOpenProcessesRequiresPositivePIDAndExactClaudeBasename(t *testing.T) {
	got := parseClaudeOpenProcesses("" +
		"0 /Applications/Claude/claude\n" +
		"not-a-pid /Applications/Claude/claude\n" +
		"101 /Applications/Claude Code/claude\n" +
		"102 /usr/local/bin/claude-helper\n" +
		"103 /Applications/Claude Code/Claude\n" +
		"104 /Applications/Claude Code/claude\n")
	if len(got) != 2 || !got[101] || !got[104] {
		t.Fatalf("parsed=%v", got)
	}
}

func TestDashboardSnapshotCursorTracksFilterAndRejectsCatalogChanges(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/state?limit=1", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", response.Code, response.Body.String())
	}
	var first dashboardState
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Sessions) != 1 || first.TotalSessions != 2 || first.NextCursor == "" {
		t.Fatalf("first page=%+v", first)
	}

	filtered := httptest.NewRequest(http.MethodGet, "/api/state?limit=1&cursor="+first.NextCursor+"&q=from", nil)
	filtered.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	filteredResponse := httptest.NewRecorder()
	handler.ServeHTTP(filteredResponse, filtered)
	if filteredResponse.Code != http.StatusBadRequest || !contains(filteredResponse.Body.String(), "invalid_cursor") {
		t.Fatalf("filter mismatch status=%d body=%s", filteredResponse.Code, filteredResponse.Body.String())
	}

	fake.sessions["third"] = surface.Session{ID: "third", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	d.discoverCatalog(context.Background())
	stale := httptest.NewRequest(http.MethodGet, "/api/state?limit=1&cursor="+first.NextCursor, nil)
	stale.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict || !contains(staleResponse.Body.String(), "catalog_changed") {
		t.Fatalf("stale cursor status=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
}

func TestDashboardCatalogPageUsesBoundedCatalogAndDoesNotPoisonFullCache(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	pageRequest := httptest.NewRequest(http.MethodGet, "/api/state?limit=1", nil)
	pageRequest.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("page status=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}
	var page dashboardState
	if err := json.Unmarshal(pageResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.TotalSessions != 2 || page.NextCursor == "" {
		t.Fatalf("page=%+v", page)
	}
	fullRequest := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	fullRequest.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	fullResponse := httptest.NewRecorder()
	handler.ServeHTTP(fullResponse, fullRequest)
	if fullResponse.Code != http.StatusOK {
		t.Fatalf("full status=%d body=%s", fullResponse.Code, fullResponse.Body.String())
	}
	var full dashboardState
	if err := json.Unmarshal(fullResponse.Body.Bytes(), &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Sessions) != 2 || full.TotalSessions != 2 {
		t.Fatalf("full state was page-contaminated: %+v", full)
	}
}

func TestDashboardEmptyCatalogPageDoesNotFallBackToSavedSessions(t *testing.T) {
	d, store, fake, from, _ := daemonFixture(t)
	if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{
		Session:               from,
		HostProject:           []byte(`{"id":"project"}`),
		Checkout:              []byte(`{}`),
		ProjectionFingerprint: `{"id":"from","surface":"codex","name":"from","cwd":"/work","status":"idle"}`,
	}, registry.CatalogEvent{DedupeKey: "session:from:catalog", Type: "session.upserted", EntityID: from.ID, Payload: []byte(`{"session":{"id":"from"}}`)}); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/state?limit=20&q=does-not-exist", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var state dashboardState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 0 || state.TotalSessions != 0 || fake.listCalls.Load() != 0 {
		t.Fatalf("empty page fell back to saved/provider sessions: sessions=%d total=%d listCalls=%d", len(state.Sessions), state.TotalSessions, fake.listCalls.Load())
	}
}

func contains(value, needle string) bool {
	return strings.Contains(value, needle)
}
