package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAWaitingSessionKeepsItsAttentionAcrossAProgressUpsert(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, store, fake, lead, _ := daemonFixture(t)
	waiting := &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1", Since: time.Now().Add(-time.Minute).UTC().Truncate(time.Second), Answerable: true}
	lead.Status, lead.WaitingOn = surface.StatusBusy, waiting
	fake.sessions = map[string]surface.Session{lead.ID: lead}
	fake.observations[lead.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1", WaitingOn: waiting}
	d.discoverCatalog(context.Background())
	lead.Plan, lead.LastActive = &surface.Plan{Done: 1, Total: 3}, time.Now().UTC()
	fake.sessions[lead.ID] = lead
	d.discoverCatalog(context.Background())
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
	var upserts []dashboardSession
	response := get("/api/v1/catalog-events?after=0")
	reader := bufio.NewReader(response.Body)
	for seq := uint64(0); seq < latest; {
		event := receiveCatalogSSE(t, reader)
		seq = event.Seq
		var data struct {
			Session dashboardSession `json:"session"`
		}
		if event.Type == "session.upserted" && json.Unmarshal(event.Data, &data) == nil && data.Session.ID == lead.ID {
			upserts = append(upserts, data.Session)
		}
	}
	response.Body.Close()
	if len(upserts) < 2 || upserts[len(upserts)-1].Plan == nil {
		t.Fatalf("streamed %d upserts of the waiting session, want the wait and then its progress", len(upserts))
	}
	for i, row := range upserts {
		if row.Attention == nil || row.Attention.Kind != "request" || row.Display == nil || row.Display.Kind != "waiting" {
			t.Errorf("upsert %d: attention %+v display %+v", i, row.Attention, row.Display)
		}
	}
	snapshot := get("/api/v1/snapshot")
	defer snapshot.Body.Close()
	var state struct {
		Sessions []dashboardSession `json:"sessions"`
	}
	if err := json.NewDecoder(snapshot.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	view := get("/api/v1/snapshot?scope=current&members=1&limit=200")
	defer view.Body.Close()
	var page SessionViewSnapshot
	if err := json.NewDecoder(view.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	streamed := upserts[len(upserts)-1].Attention
	detail := get("/api/v1/session?id=" + lead.ID)
	defer detail.Body.Close()
	var opened struct {
		Attention *SessionAttention `json:"attention"`
	}
	if err := json.NewDecoder(detail.Body).Decode(&opened); err != nil {
		t.Fatal(err)
	}
	if opened.Attention == nil || streamed == nil || *opened.Attention != *streamed {
		t.Errorf("detail attention %+v, stream attention %+v", opened.Attention, streamed)
	}
	for name, rows := range map[string][]dashboardSession{"snapshot": state.Sessions, "view": page.Sessions} {
		found := false
		for _, row := range rows {
			if row.ID != lead.ID {
				continue
			}
			found = true
			if row.Attention == nil || streamed == nil || *row.Attention != *streamed {
				t.Errorf("%s attention %+v, stream attention %+v", name, row.Attention, streamed)
			}
		}
		if !found {
			t.Errorf("%s does not list the waiting session", name)
		}
	}
}
