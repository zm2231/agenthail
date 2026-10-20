package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	registrypkg "github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestMobileCreationOptionsAndAuthorizedCreation(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, authorized := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session-options", nil)
		if authorized {
			request.Header.Set("Authorization", "Bearer secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !authorized {
			if response.Code != http.StatusUnauthorized {
				t.Fatal(response.Code)
			}
			continue
		}
		var options struct {
			Surfaces []struct {
				ID        string `json:"id"`
				Workspace bool   `json:"workspace"`
			} `json:"surfaces"`
			Projects []launchProject `json:"projects"`
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, retired := raw["workspaces"]; retired || raw["projects"] == nil {
			t.Fatalf("session options keys=%v", raw)
		}
		if err := json.Unmarshal(response.Body.Bytes(), &options); err != nil {
			t.Fatal(err)
		}
		if len(options.Surfaces) == 0 {
			t.Fatal("missing starters")
		}
	}
	for _, authorized := range []bool{false, true} {
		body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir()})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
		if authorized {
			request.Header.Set("Authorization", "Bearer secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !authorized {
			if response.Code != http.StatusUnauthorized || len(fake.startOptions) != 0 {
				t.Fatal(response.Code)
			}
			continue
		}
		if response.Code != http.StatusCreated || len(fake.startOptions) != 1 {
			t.Fatal(response.Code, response.Body.String())
		}
		if _, err := registry.Session("started"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMobileQueueIncludesFailedRowAndRetriesThroughActionAPI(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	id, err := registry.QueueMessageWithKey("from", "Failed instruction", "failure-probe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ClaimNextMessage("from", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.DeadLetterMessage(id, errors.New("delivery rejected [invalid request]: bad")); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []dashboardQueue `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.ID == id && item.Status == "dead" {
			if item.Historical || item.Evidence != surface.EvidenceFailed {
				t.Fatalf("failed row=%+v", item)
			}
			found = true
		}
	}
	if !found {
		t.Fatal(response.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"action": "queue-retry", "queueId": id})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	item, err := registry.QueueItem(id, nil)
	if err != nil || item.Status != "pending" {
		t.Fatalf("%+v %v", item, err)
	}
}

func TestLaunchProjectsListRecentProjectsThenAddedFolders(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	now := time.Now().UTC()
	record := func(id, project string, lastActive time.Time, subagent bool) {
		t.Helper()
		session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: id, Cwd: "/work/" + id, Status: surface.StatusIdle, LastActive: lastActive}
		if subagent {
			session.Subagent = &surface.Subagent{ParentID: "older", RootID: "older", Depth: 1}
		}
		if _, _, err := reg.RecordCatalogSession(registrypkg.CatalogSessionState{Session: session, Project: []byte(project), Checkout: []byte(`{}`), ObservedAt: now, ProjectionFingerprint: `{}`}, registrypkg.CatalogEvent{DedupeKey: "session.upserted:" + id, Type: "session.upserted", EntityID: id, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	record("older", `{"id":"repo:github.com/example/api","localId":"implicit:/work/api","displayName":"api","rootPath":"/work/api","kind":"git"}`, now.Add(-2*time.Hour), false)
	record("newer", `{"id":"implicit:/work/web","localId":"implicit:/work/web","displayName":"web","rootPath":"/work/web","kind":"git"}`, now.Add(-time.Hour), false)
	record("worktree", `{"id":"repo:github.com/example/api","localId":"implicit:/work/api","displayName":"api","rootPath":"/work/api","kind":"git"}`, now.Add(-30*time.Minute), false)
	record("helper", `{"id":"implicit:/work/tools","localId":"implicit:/work/tools","displayName":"tools","rootPath":"/work/tools","kind":"git"}`, now, true)
	record("hidden", `{"id":"implicit:/work/old","localId":"implicit:/work/old","displayName":"old","rootPath":"/work/old","kind":"git"}`, now, false)
	record("loose", `null`, now, false)
	if err := reg.ArchiveProject(registrypkg.ProjectRecord{ID: "implicit:/work/old", WorkspaceRoot: "/work/old", Title: "old"}, now); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddProject(registrypkg.ProjectRecord{ID: "p-notes", WorkspaceRoot: "/work/notes", Title: "notes", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	projects, err := d.launchProjects()
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, project := range projects {
		got = append(got, project.DisplayName+"="+project.RootPath)
	}
	if strings.Join(got, ",") != "api=/work/api,web=/work/web,notes=/work/notes" {
		t.Fatalf("projects=%v", got)
	}
}
