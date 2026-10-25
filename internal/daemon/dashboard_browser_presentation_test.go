package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func recordBrowserPresentation(t *testing.T, d *Daemon, row dashboardSession) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := json.Marshal(row.Project)
	session := surface.Session{ID: row.ID, Name: row.Name, Surface: row.Surface, RuntimeKind: surface.RuntimeCodexDesktop, Status: row.Status, Subagent: row.Subagent, WaitingOn: row.WaitingOn, LastActive: row.LastActive}
	if err := d.Registry.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Registry.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: project, Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: row.ID, Type: "session.upserted", EntityID: row.ID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserMetadataDoesNotHydrateSessions(t *testing.T) {
	d, store, fake, _, _ := daemonFixture(t)
	before := fake.listCalls
	row := surface.Session{ID: "bad-full-row", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
	if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: row, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"bad-full-row","surface":"codex","holders":"not a holder array"}`}, registry.CatalogEvent{DedupeKey: row.ID, Type: "session.upserted", EntityID: row.ID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	state, err := d.readBrowserMetadata(context.Background())
	if err != nil || len(state.Sessions) != 0 || fake.listCalls != before {
		t.Fatalf("metadata=%+v err=%v calls=%d", state, err, fake.listCalls)
	}
	router := dashboardRouter(d)
	for _, query := range []string{"metadata=0", "metadata=1&scope=all", "metadata=1&sessionId=x", "metadata=1&cursor=x"} {
		if response := serveDashboardRequest(router, "GET", "/api/state?"+query, ""); response.Code != 400 {
			t.Fatalf("%s: %d", query, response.Code)
		}
	}
}

func TestBrowserDetailIncludesHiddenAndHistoryWithoutChangingCounts(t *testing.T) {
	d, store, _, from, _ := daemonFixture(t)
	recordBrowserPresentation(t, d, dashboardSession{ID: "root", Surface: surface.KindCodex, Status: surface.StatusIdle})
	recordBrowserPresentation(t, d, dashboardSession{ID: "hidden", Surface: surface.KindCodex, Status: surface.StatusIdle, WaitingOn: &surface.Waiting{ID: "ask", Answerable: true}, Subagent: &surface.Subagent{ParentID: "root", RootID: "root"}})
	if err := store.ArchiveSession("hidden", time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	router := dashboardRouter(d)
	for _, id := range []string{"hidden", from.ID} {
		response := serveDashboardRequest(router, "GET", "/api/session?id="+id, "")
		var detail struct {
			Session  dashboardSession     `json:"session"`
			Position PresentationPosition `json:"presentationPosition"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.Session.ID != id || detail.Session.Display == nil || detail.Position.HostID != d.hostIdentity().ID {
			t.Fatalf("%s: %d %s", id, response.Code, response.Body.String())
		}
		if id == "hidden" && detail.Session.Display.Label != "Waiting on you" {
			t.Fatalf("hidden display: %+v", detail.Session.Display)
		}
	}
	after, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 200})
	if err != nil || before.View.Counts["all"] != after.View.Counts["all"] {
		t.Fatalf("history changed counts: before=%+v after=%+v err=%v", before.View.Counts, after.View.Counts, err)
	}
}

func TestBrowserViewKeepsEmptyAddedProjects(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	if err := store.AddProject(registry.ProjectRecord{ID: "empty", WorkspaceRoot: t.TempDir(), Title: "Empty", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	state, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 200})
	if err != nil || len(state.View.Projects) != 1 || state.View.Projects[0].Name != "Empty" || state.View.Projects[0].Counts.Families != 0 {
		t.Fatalf("state=%+v err=%v", state.View, err)
	}
}
