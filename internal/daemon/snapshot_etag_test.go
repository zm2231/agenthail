package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func getSnapshot(t *testing.T, handler http.Handler, path, tag string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer secret")
	if tag != "" {
		request.Header.Set("If-None-Match", tag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAnUnchangedSnapshotIsNotSentAgainToAClientThatHoldsIt(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	dashboard := &dashboardServer{token: "secret"}
	handler := d.dashboardHandler(dashboard)
	first := getSnapshot(t, handler, "/api/v1/snapshot", "")
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || tag == "" || first.Body.Len() == 0 {
		t.Fatalf("first status=%d etag=%q", first.Code, tag)
	}
	if cached := getSnapshot(t, handler, "/api/v1/snapshot", ""); cached.Code != http.StatusOK || cached.Header().Get("ETag") != tag {
		t.Fatalf("a cached snapshot status=%d etag=%q, want %q", cached.Code, cached.Header().Get("ETag"), tag)
	}
	held := getSnapshot(t, handler, "/api/v1/snapshot", tag)
	if held.Code != http.StatusNotModified || held.Body.Len() != 0 || held.Header().Get("ETag") != tag {
		t.Fatalf("a held snapshot status=%d body=%d etag=%q", held.Code, held.Body.Len(), held.Header().Get("ETag"))
	}
	if filtered := getSnapshot(t, handler, "/api/v1/snapshot?scope=all", tag); filtered.Code != http.StatusOK || filtered.Header().Get("ETag") != "" {
		t.Fatalf("a filtered snapshot status=%d etag=%q, want a full body without a tag", filtered.Code, filtered.Header().Get("ETag"))
	}
	if fresh := getSnapshot(t, handler, "/api/v1/snapshot?fresh=1", tag); fresh.Code != http.StatusOK || fresh.Body.Len() == 0 {
		t.Fatalf("a fresh snapshot status=%d", fresh.Code)
	}
	if rebuilt := getSnapshot(t, handler, "/api/v1/snapshot", tag); rebuilt.Code != http.StatusOK {
		t.Fatalf("a snapshot after a fresh rebuild status=%d, want the rebuilt body", rebuilt.Code)
	}
	tag = getSnapshot(t, handler, "/api/v1/snapshot", "").Header().Get("ETag")
	dashboard.invalidate()
	if changed := getSnapshot(t, handler, "/api/v1/snapshot", tag); changed.Code != http.StatusOK || changed.Header().Get("ETag") == tag {
		t.Fatalf("a snapshot after a state change status=%d etag=%q", changed.Code, changed.Header().Get("ETag"))
	}
	tag = getSnapshot(t, handler, "/api/v1/snapshot", "").Header().Get("ETag")
	fake.sessions["third"] = surface.Session{ID: "third", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	d.discoverCatalog(context.Background())
	if changed := getSnapshot(t, handler, "/api/v1/snapshot", tag); changed.Code != http.StatusOK || changed.Header().Get("ETag") == tag {
		t.Fatalf("a snapshot after a catalog change status=%d etag=%q", changed.Code, changed.Header().Get("ETag"))
	}
}
