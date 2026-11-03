package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASessionViewPageCarriesEveryFamilyMemberWhenAsked(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	now := time.Now().UTC()
	for _, row := range []dashboardSession{{ID: "root", Name: "Root", Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: now}, {ID: "child", Name: "Child", Surface: surface.KindCodex, Status: surface.StatusBusy, Subagent: &surface.Subagent{ParentID: "root", RootID: "root"}}} {
		raw, _ := json.Marshal(row)
		session := surface.Session{ID: row.ID, Name: row.Name, Surface: row.Surface, RuntimeKind: surface.RuntimeCodexDesktop, Status: row.Status, Subagent: row.Subagent, LastActive: row.LastActive}
		if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: row.ID, Type: "session.upserted", EntityID: row.ID, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(query string) (SessionViewSnapshot, *httptest.ResponseRecorder) {
		t.Helper()
		w := httptest.NewRecorder()
		d.sessionViewHandler(w, httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?"+query, nil))
		var state SessionViewSnapshot
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
		}
		return state, w
	}
	ids := func(state SessionViewSnapshot) map[string]bool {
		out := map[string]bool{}
		for _, row := range state.Sessions {
			out[row.ID] = true
		}
		return out
	}

	roots, plain := get("scope=all")
	if got := ids(roots); len(got) != 1 || !got["root"] {
		t.Fatalf("a plain page carried %v", got)
	}
	members, withMembers := get("scope=all&members=1")
	if got := ids(members); len(got) != 2 || !got["root"] || !got["child"] {
		t.Fatalf("a page with members carried %v", got)
	}
	if plain.Header().Get("ETag") == withMembers.Header().Get("ETag") {
		t.Fatal("pages with and without members share an ETag")
	}
	if _, bad := get("scope=all&members=2"); bad.Code != http.StatusBadRequest {
		t.Fatalf("members=2 answered %d", bad.Code)
	}
}

func TestTheDaemonSpeaksProtocolsOneAndTwo(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var version apiVersionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &version); err != nil {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	if version.Protocol != 2 || version.MinimumProtocol != 1 || version.MaximumProtocol != 2 {
		t.Fatalf("version %+v", version)
	}
}
