package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheCatalogStreamAndTheViewAgreeOnPlacement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, store, fake, lead, _ := daemonFixture(t)
	child := surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "worker", Status: surface.StatusBusy, Source: "subAgent", Transport: "desktop", Subagent: &surface.Subagent{ParentID: lead.ID, RootID: lead.ID, Depth: 1}}
	fake.sessions = map[string]surface.Session{lead.ID: lead, child.ID: child}
	d.discoverCatalog(context.Background())
	d.publishCatalogPresence()
	_, latest, err := store.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "dashboard-secret"}))
	defer server.Close()
	get := func(path string) *http.Response {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer dashboard-secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %v", path, err)
		}
		return response
	}
	streamed := map[string]string{}
	streamedRows := map[string]dashboardSession{}
	response := get(fmt.Sprintf("/api/v1/catalog-events?after=%d", 0))
	reader := bufio.NewReader(response.Body)
	for seq := uint64(0); seq < latest; {
		event := receiveCatalogSSE(t, reader)
		seq = event.Seq
		var data struct {
			Session dashboardSession `json:"session"`
		}
		if event.Type == "session.upserted" && json.Unmarshal(event.Data, &data) == nil {
			streamed[data.Session.ID] = data.Session.Placement
			streamedRows[data.Session.ID] = data.Session
		}
	}
	response.Body.Close()
	view := get("/api/v1/snapshot?scope=current&members=1&limit=200")
	defer view.Body.Close()
	var page SessionViewSnapshot
	if err := json.NewDecoder(view.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) < 2 {
		t.Fatalf("view listed %d rows, want the lead and its working child", len(page.Sessions))
	}
	for _, row := range page.Sessions {
		if row.Placement == "" || streamed[row.ID] != row.Placement {
			t.Errorf("%s: view inputs %+v, stream inputs %+v", row.ID, placementOf(row), placementOf(streamedRows[row.ID]))
		}
	}
}
