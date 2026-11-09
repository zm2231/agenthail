package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func folderScopedQueue(t *testing.T) (http.Handler, map[string]int64) {
	t.Helper()
	d, reg, _, _, _ := daemonFixture(t)
	if err := reg.AddProject(registry.ProjectRecord{ID: "p-root", WorkspaceRoot: "/work/root", Title: "root"}); err != nil {
		t.Fatal(err)
	}
	queued := map[string]int64{}
	for id, cwd := range map[string]string{"nested": "/work/root/nested", "sibling": "/work/root/sibling", "other": "/work/other"} {
		session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: cwd}
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		projection := `{}`
		if id != "other" {
			projection = `{"project":{"id":"p-root","displayName":"root"}}`
		}
		if _, _, err := reg.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: projection}, registry.CatalogEvent{DedupeKey: "session:" + id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if id == "sibling" {
			continue
		}
		queueID, err := reg.QueueMessageWithOptions(id, "for "+id, "", surface.SendOptions{})
		if err != nil {
			t.Fatal(err)
		}
		queued[id] = queueID
	}
	return dashboardRouter(d), queued
}

func queuedMessages(t *testing.T, handler http.Handler, query string) []string {
	t.Helper()
	response := serveDashboardRequest(handler, http.MethodGet, "/api/v1/queue?"+query, "")
	if response.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", query, response.Code, response.Body.String())
	}
	var page mobileQueuePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	messages := []string{}
	for _, item := range page.Items {
		messages = append(messages, item.Message)
	}
	return messages
}

func TestTheQueueListsOnlyTheProjectThatHoldsAFolder(t *testing.T) {
	handler, queued := folderScopedQueue(t)
	for _, query := range []string{"cwd=" + url.QueryEscape("/work/root/nested"), "projectId=p-root", fmt.Sprintf("id=%d&projectId=p-root", queued["nested"])} {
		if got := queuedMessages(t, handler, query); len(got) != 1 || got[0] != "for nested" {
			t.Fatalf("%s: %v", query, got)
		}
	}
	if got := queuedMessages(t, handler, fmt.Sprintf("id=%d&cwd=%s", queued["other"], url.QueryEscape("/work/root"))); len(got) != 0 {
		t.Fatalf("an item outside the folder's project: %v", got)
	}
	for query, want := range map[string]int{
		"cwd=" + url.QueryEscape("/work/elsewhere"): http.StatusNotFound,
		"cwd=work/root": http.StatusBadRequest,
		"cwd=" + url.QueryEscape("/work/root") + "&projectId=p-root":                      http.StatusBadRequest,
		fmt.Sprintf("id=%d&cwd=%s", queued["nested"], url.QueryEscape("/work/elsewhere")): http.StatusNotFound,
	} {
		if response := serveDashboardRequest(handler, http.MethodGet, "/api/v1/queue?"+query, ""); response.Code != want {
			t.Fatalf("%s: status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
}

func TestTheSessionListTakesAFolderForItsProject(t *testing.T) {
	handler, _ := folderScopedQueue(t)
	folder := url.QueryEscape("/work/root/nested")
	ids := []string{}
	cursor := ""
	for page := 0; page < 3; page++ {
		target := "/api/v1/snapshot?scope=all&limit=1&cwd=" + folder
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		response := serveDashboardRequest(handler, http.MethodGet, target, "")
		var state SessionViewSnapshot
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil || state.View == nil || state.View.Filters.ProjectID != "p-root" {
			t.Fatalf("page %d: status=%d body=%s", page, response.Code, response.Body.String())
		}
		for _, row := range state.Sessions {
			ids = append(ids, row.ID)
		}
		if cursor = state.NextCursor; cursor == "" {
			break
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"nested", "sibling"}) {
		t.Fatalf("sessions over the pages: %v", ids)
	}
	if response := serveDashboardRequest(handler, http.MethodGet, "/api/v1/snapshot?scope=all&cwd="+url.QueryEscape("/work/elsewhere"), ""); response.Code != http.StatusNotFound {
		t.Fatalf("unheld folder: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSavingAnEmptyNameClearsTheConversationName(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	for _, alias := range []string{"shipper", ""} {
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/action", `{"action":"alias","sessionId":"from","alias":"`+alias+`"}`); response.Code != http.StatusOK {
			t.Fatalf("alias %q: status=%d body=%s", alias, response.Code, response.Body.String())
		}
	}
	if name, err := reg.ReverseAlias("from"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, err := reg.LookupAlias("shipper"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("@shipper still resolves: %v", err)
	}
	if _, err := reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: "p-root", ProjectRoot: "/work/root", Name: "root-lead", Surface: "codex", Cwd: "/work/root"}, registry.CoordinatorChange{SessionID: "from", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/action", `{"action":"alias","sessionId":"from","alias":""}`); response.Code != http.StatusConflict {
		t.Fatalf("clearing a coordinator's name: status=%d body=%s", response.Code, response.Body.String())
	}
	if name, err := reg.ReverseAlias("from"); err != nil || name != "root-lead" {
		t.Fatalf("coordinator name=%q err=%v", name, err)
	}
}
