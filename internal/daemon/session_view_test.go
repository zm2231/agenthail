package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestPresentationScopesCountsAndCollapse(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	project := &CatalogProject{ID: "project", DisplayName: "Project"}
	rows := []dashboardSession{}
	for i := range 7 {
		rows = append(rows, dashboardSession{ID: fmt.Sprint(i), Status: surface.StatusIdle, Project: project, LastActive: now.Add(-time.Duration(i) * time.Minute)})
	}
	rows = append(rows, dashboardSession{ID: "old-working", Status: surface.StatusBusy, Project: project, LastActive: now.Add(-48 * time.Hour)}, dashboardSession{ID: "old-queue", Status: surface.StatusIdle, QueueCount: 1, Project: project}, dashboardSession{ID: "waiting-child", Status: surface.StatusIdle, WaitingOn: &surface.Waiting{ID: "q"}, Subagent: &surface.Subagent{ParentID: "6", RootID: "6"}}, dashboardSession{ID: "archived-child", Archived: true, Subagent: &surface.Subagent{ParentID: "6", RootID: "6"}}, dashboardSession{ID: "orphan", Status: surface.StatusBusy, Subagent: &surface.Subagent{ParentID: "missing"}})
	plan := newSessionViewPlan(rows, "recent", SessionViewFilters{}, now)
	if got := plan.view.Counts; got["current"] != (SessionViewCount{3, 3}) || got["recent"] != (SessionViewCount{9, 10}) || got["all"] != (SessionViewCount{9, 10}) || got["archived"] != (SessionViewCount{1, 1}) {
		t.Fatalf("counts=%+v", got)
	}
	got := plan.view.Projects[0].CollapsedFamilyIDs
	want := []string{"6", "old-working", "old-queue", "0", "1", "2", "3", "4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collapsed=%v want=%v", got, want)
	}
	archived := newSessionViewPlan(rows, "archived", SessionViewFilters{}, now)
	if !archived.view.Families[0].Members[0].ContextOnly || archived.view.Families[0].Members[1].ID != "archived-child" {
		t.Fatalf("archived=%+v", archived.view.Families)
	}
	boundary := dashboardSession{ID: "boundary", LastActive: now.Add(-24 * time.Hour)}
	if !presentationMatches(boundary, "recent", now) || presentationMatches(boundary, "recent", now.Add(time.Nanosecond)) {
		t.Fatal("recent boundary")
	}
}

func TestPresentationHTTPPagingHydrationAndReadOnly(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	now := time.Now().UTC()
	for _, row := range []dashboardSession{{ID: "root", Name: "Root", Surface: surface.KindCodex, Status: surface.StatusIdle}, {ID: "child", Name: "Child", Surface: surface.KindCodex, Status: surface.StatusBusy, Subagent: &surface.Subagent{ParentID: "root", RootID: "root"}}, {ID: "other", Name: "Other", Surface: surface.KindCodex, Status: surface.StatusIdle}} {
		raw, _ := json.Marshal(row)
		session := surface.Session{ID: row.ID, Name: row.Name, Surface: row.Surface, RuntimeKind: surface.RuntimeCodexDesktop, Status: row.Status, Subagent: row.Subagent}
		if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: row.ID, Type: "session.upserted", EntityID: row.ID, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	_, before, _ := store.CatalogState()
	get := func(query string) (SessionViewSnapshot, int) {
		t.Helper()
		w := httptest.NewRecorder()
		d.sessionViewHandler(w, httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?"+query, nil))
		var state SessionViewSnapshot
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Log(w.Body.String())
		}
		return state, w.Code
	}
	first, status := get("scope=all&limit=1")
	if status != 200 || len(first.Sessions) != 1 || first.View.Counts["all"] != (SessionViewCount{2, 3}) || first.NextCursor == "" {
		t.Fatalf("first=%+v status=%d", first, status)
	}
	child, status := get("scope=all&revision=" + first.View.Revision + "&sessionId=child")
	if status != 200 || len(child.Sessions) != 1 || child.Sessions[0].Display.Label != "Working" {
		t.Fatalf("child=%+v", child)
	}
	_, status = get("scope=all&limit=1&cursor=" + url.QueryEscape(first.NextCursor))
	if status != 200 {
		t.Fatal(status)
	}
	_, after, _ := store.CatalogState()
	if before != after {
		t.Fatalf("read wrote catalog %d -> %d", before, after)
	}
	if err := store.ArchiveSession("other", now); err != nil {
		t.Fatal(err)
	}
	_, status = get("scope=all&limit=1&cursor=" + url.QueryEscape(first.NextCursor))
	if status != 409 {
		t.Fatalf("archive failed to invalidate cursor: %d", status)
	}
	for _, query := range []string{"scope=bad", "scope=all&parentId=root", "scope=all&limit=0", "scope=all&cursor=bad", "scope=all&sessionId=child"} {
		_, status = get(query)
		if status != 400 {
			t.Fatalf("%s: %d", query, status)
		}
	}
	_, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "recent", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPresentationAttentionETagAndQueueChanges(t *testing.T) {
	d, store, fake, from, to := daemonFixture(t)
	d.discoverCatalog(context.Background())
	intent, err := store.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: from.ID, TargetSessionID: to.ID, Status: registry.DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailDeliveryIntent(intent.ID, "not delivered"); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	first := getSnapshot(t, handler, "/api/v1/snapshot?scope=current&attention=1&limit=1", "")
	var state SessionViewSnapshot
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &state) != nil || state.View.Summary.AttentionFamilies != 1 || len(state.Sessions) != 1 || state.Sessions[0].ID != from.ID || state.Sessions[0].Attention.Kind != "outgoing" {
		t.Fatal(first.Body.String())
	}
	tag := first.Header().Get("ETag")
	if got := getSnapshot(t, handler, "/api/v1/snapshot?scope=current&attention=1&limit=1", tag); got.Code != 304 {
		t.Fatalf("unchanged=%d %s", got.Code, got.Body.String())
	}
	if got := getSnapshot(t, handler, "/api/v1/snapshot?scope=current&attention=1&limit=2", tag); got.Code != 200 {
		t.Fatalf("limit variant=%d", got.Code)
	}
	d.presentation.epoch = "synthetic-next-boot"
	if got := getSnapshot(t, handler, "/api/v1/snapshot?scope=current&attention=1&limit=1", tag); got.Code != 200 || got.Header().Get("ETag") == tag {
		t.Fatal("restart did not invalidate ETag")
	}
	tag = getSnapshot(t, handler, "/api/v1/snapshot?scope=current&attention=1&limit=1", "").Header().Get("ETag")
	beforeCalls := fake.listCalls.Load()
	if err := store.QueueMessage(to.ID, "queued"); err != nil {
		t.Fatal(err)
	}
	got := getSnapshot(t, handler, "/api/v1/snapshot?scope=current&attention=1&limit=1", tag)
	if got.Code != 200 || got.Header().Get("ETag") == tag {
		t.Fatalf("queue invalidation=%d", got.Code)
	}
	if fake.listCalls.Load() != beforeCalls {
		t.Fatal("list read called provider")
	}
}

func TestPresentationFiltersAndSelectionUseMatchingMembers(t *testing.T) {
	now := time.Now().UTC()
	rows := []dashboardSession{
		{ID: "root", Name: "root", Status: surface.StatusIdle, LastActive: now},
		{ID: "child", Name: "needle", Surface: surface.KindClaude, Status: surface.StatusBusy, Subagent: &surface.Subagent{ParentID: "root"}},
		{ID: "cycle-a", Subagent: &surface.Subagent{ParentID: "cycle-b"}},
		{ID: "cycle-b", Subagent: &surface.Subagent{ParentID: "cycle-a"}},
	}
	plan := newSessionViewPlan(rows, "current", SessionViewFilters{Query: "needle", Surface: "claude"}, now)
	if plan.view.Counts["current"] != (SessionViewCount{1, 1}) || !plan.view.Families[0].Members[0].ContextOnly {
		t.Fatalf("view=%+v", plan.view)
	}
	plan = newSessionViewPlan(rows, "current", SessionViewFilters{Query: "root", Surface: "claude"}, now)
	if len(plan.view.Families) != 0 {
		t.Fatal("different members satisfied refinements")
	}
	rows = nil
	for i := 0; i < 8; i++ {
		rows = append(rows, dashboardSession{ID: fmt.Sprint(i), LastActive: now.Add(-time.Duration(i) * time.Minute)})
	}
	plan = newSessionViewPlan(rows, "all", SessionViewFilters{SelectedID: "7"}, now)
	if got := plan.view.Projects[0].CollapsedFamilyIDs; !reflect.DeepEqual(got, []string{"0", "1", "2", "3", "4", "7"}) {
		t.Fatal(got)
	}
}
