package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func apiErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("not a typed error: %d %s", response.Code, response.Body)
	}
	return envelope.Error.Code
}

func TestProjectAddAndRenameRepeatSafelyAndAnswerWithTypedErrors(t *testing.T) {
	d, _, _, repo := projectsFixture(t)
	folder := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		Project CatalogProject `json:"project"`
		Created bool           `json:"created"`
	}
	add := func(body string) (answer, *httptest.ResponseRecorder) {
		t.Helper()
		response := runDashboardAction(t, d, body)
		var got answer
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
		}
		return got, response
	}
	first, response := add(`{"action":"project-add","path":"` + folder + `","title":"Notes"}`)
	if response.Code != http.StatusOK || !first.Created || first.Project.DisplayName != "Notes" {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	again, response := add(`{"action":"project-add","path":"` + folder + `","title":"Other"}`)
	if response.Code != http.StatusOK || again.Created || again.Project.ID != first.Project.ID || again.Project.DisplayName != "Notes" {
		t.Fatalf("add again: %d %s", response.Code, response.Body)
	}
	if _, response := add(`{"action":"project-add","path":"` + filepath.Join(folder, "missing") + `"}`); response.Code != http.StatusBadRequest || apiErrorCode(t, response) != "folder_not_found" {
		t.Fatalf("missing folder: %d %s", response.Code, response.Body)
	}

	rename := func(projectID, title string) *httptest.ResponseRecorder {
		t.Helper()
		return runDashboardAction(t, d, `{"action":"project-rename","projectId":"`+projectID+`","title":"`+title+`"}`)
	}
	for range 2 {
		if response := rename(first.Project.ID, "Field notes"); response.Code != http.StatusOK {
			t.Fatalf("rename: %d %s", response.Code, response.Body)
		}
	}
	d.discoverCatalog(context.Background())
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, project := range state.Projects {
		if project.ID == first.Project.ID {
			listed = project.DisplayName == "Field notes"
		}
	}
	if !listed {
		t.Fatal("the snapshot does not show the new name")
	}
	if response := rename(first.Project.ID, "  "); response.Code != http.StatusBadRequest || apiErrorCode(t, response) != "invalid_request" {
		t.Fatalf("blank title: %d %s", response.Code, response.Body)
	}
	if response := rename("p-nowhere", "x"); response.Code != http.StatusNotFound || apiErrorCode(t, response) != "project_not_found" {
		t.Fatalf("unknown project: %d %s", response.Code, response.Body)
	}
	implicit := storedProjection(t, d.Registry, "from").Project.ID
	if response := rename(implicit, "x"); response.Code != http.StatusConflict || apiErrorCode(t, response) != "project_not_added" {
		t.Fatalf("implicit %s: %d %s", repo, response.Code, response.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"p-nowhere"}`); response.Code != http.StatusNotFound || apiErrorCode(t, response) != "project_not_found" {
		t.Fatalf("remove unknown: %d %s", response.Code, response.Body)
	}
}

func TestALeadSetTwiceOrClearedWhenAbsentChangesNothing(t *testing.T) {
	d, r, _, repo := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	d.discoverCatalog(context.Background())
	projectID := storedProjection(t, r, "from").Project.ID
	clear := func() *httptest.ResponseRecorder {
		return runDashboardAction(t, d, `{"action":"coordinator-clear","projectId":"`+projectID+`"}`)
	}
	if response := clear(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"cleared":null`) {
		t.Fatalf("clear with no lead: %d %s", response.Code, response.Body)
	}
	var first time.Time
	for attempt := range 2 {
		response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"widget-lead"}`)
		var set struct {
			Coordinator registry.CoordinatorSpec `json:"coordinator"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &set) != nil {
			t.Fatalf("set: %d %s", response.Code, response.Body)
		}
		if set.Coordinator.ProjectID != projectID {
			t.Fatalf("lead of %s, project %s", set.Coordinator.ProjectID, projectID)
		}
		current, err := r.Coordinator(projectID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			first = current.ChangedAt
		} else if !current.ChangedAt.Equal(first) {
			t.Fatalf("setting the same lead again changed it: %v then %v", first, current.ChangedAt)
		}
	}
	other := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"to","name":"other-lead"}`)
	if other.Code != http.StatusConflict || apiErrorCode(t, other) != "coordinator_exists" || strings.Contains(other.Body.String(), "--") {
		t.Fatalf("takeover: %d %s", other.Code, other.Body)
	}
	if response := clear(); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"widget-lead"`) {
		t.Fatalf("clear: %d %s", response.Code, response.Body)
	}
}

func TestSessionOptionsSuggestOnlyRecentSessionFoldersOutsideProjects(t *testing.T) {
	d, r, fake, _ := projectsFixture(t)
	base := t.TempDir()
	now := time.Now().UTC()
	for index, name := range []string{"scratch", "notes", "notes/inner", "added", "added/deeper"} {
		folder := filepath.Join(base, name)
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		session := surface.Session{ID: "loose-" + strings.ReplaceAll(name, "/", "-"), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: folder, LastActive: now.Add(time.Duration(index) * time.Minute)}
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+filepath.Join(base, "added")+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	d.discoverCatalog(context.Background())
	response := httptest.NewRecorder()
	d.sessionOptionsHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/session-options", nil))
	var options struct {
		Folders  []string `json:"folders"`
		Projects []struct {
			RootPath string `json:"rootPath"`
			Added    bool   `json:"added"`
		} `json:"projects"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &options) != nil {
		t.Fatalf("options: %d %s", response.Code, response.Body)
	}
	want := []string{filepath.Join(base, "notes", "inner"), filepath.Join(base, "notes"), filepath.Join(base, "scratch")}
	if strings.Join(options.Folders, ",") != strings.Join(want, ",") {
		t.Fatalf("folders=%v want %v", options.Folders, want)
	}
	added := 0
	for _, project := range options.Projects {
		if project.Added {
			added++
		}
	}
	if added != 1 {
		t.Fatalf("projects=%+v", options.Projects)
	}
}

func TestFederationAddsAndRemovesAProjectOnTheHostItNames(t *testing.T) {
	local, remote, record := pairedFederation(t)
	cwd := remote.fake.sessions["remote-a"].Cwd
	missing := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-add", "host": "vps", "path": filepath.Join(cwd, "not-there")})
	if missing.Code != http.StatusBadRequest || apiErrorCode(t, missing) != "folder_not_found" {
		t.Fatalf("missing folder on the host: %d %s", missing.Code, missing.Body)
	}
	unknown := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-add", "host": "nowhere", "path": cwd})
	if unknown.Code != http.StatusBadRequest || apiErrorCode(t, unknown) != "invalid_request" || !strings.Contains(unknown.Body.String(), "nowhere") {
		t.Fatalf("unknown host: %d %s", unknown.Code, unknown.Body)
	}
	added := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-add", "host": "vps", "path": cwd, "title": "Server work"})
	var answer struct {
		Project CatalogProject `json:"project"`
	}
	if added.Code != http.StatusOK || json.Unmarshal(added.Body.Bytes(), &answer) != nil {
		t.Fatalf("add: %d %s", added.Code, added.Body)
	}
	if records, err := local.reg.ListProjects(); err != nil || len(records) != 0 {
		t.Fatalf("the project was added on the wrong host: %+v err=%v", records, err)
	}
	if records, err := remote.reg.ListProjects(); err != nil || len(records) != 1 || records[0].Title != "Server work" {
		t.Fatalf("remote projects: %+v err=%v", records, err)
	}
	qualified := hosts.Qualify(record.ID, answer.Project.LocalID)
	if renamed := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-rename", "projectId": qualified, "title": "Builds"}); renamed.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body)
	}
	if records, err := remote.reg.ListProjects(); err != nil || len(records) != 1 || records[0].Title != "Builds" {
		t.Fatalf("remote projects after rename: %+v err=%v", records, err)
	}
	if removed := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-remove", "projectId": qualified}); removed.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", removed.Code, removed.Body)
	}
	if records, err := remote.reg.ListProjects(); err != nil || len(records) != 0 {
		t.Fatalf("remote projects after remove: %+v err=%v", records, err)
	}
}

func TestFederationRenamesOneRepositoryProjectOnEveryHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	remote := newFederationNode(t, "remote-a", "remote-b")
	moveSessions(t, remote, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
	remote.serve(t, nil)
	local := newFederationNode(t, "local-a")
	localRoot := cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget"))
	moveSessions(t, local, filepath.Join(localRoot, "src"))
	local.start(t, testRoots(remote.server))
	local.pair(t, remote, "vps")
	for node, folder := range map[*federationNode]string{local: filepath.Join(localRoot, "src"), remote: remote.fake.sessions["remote-a"].Cwd} {
		if response := node.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-add", "path": folder}); response.Code != http.StatusOK {
			t.Fatalf("add: %d %s", response.Code, response.Body)
		}
		node.daemon.discoverCatalog(context.Background())
	}
	projectID := storedProjection(t, local.reg, "local-a").Project.ID
	// A rename reaches the paired hosts this host lists the project on, so it
	// waits for this host to mirror the server's checkout.
	testkit.Eventually(t, "one project with both checkouts", 10*time.Second, func() bool {
		for _, project := range local.snapshot(t).Projects {
			if project.ID == projectID {
				return len(project.Checkouts) == 2
			}
		}
		return false
	})
	renamed := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-rename", "projectId": projectID, "title": "Gadget sources"})
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), `"host":"vps","ok":true`) {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body)
	}
	for _, node := range []*federationNode{local, remote} {
		if records, err := node.reg.ListProjects(); err != nil || len(records) != 1 || records[0].Title != "Gadget sources" {
			t.Fatalf("projects: %+v err=%v", records, err)
		}
		node.daemon.discoverCatalog(context.Background())
	}
	testkit.Eventually(t, "one project under the new name", 10*time.Second, func() bool {
		for _, project := range local.snapshot(t).Projects {
			if project.ID == projectID {
				return project.DisplayName == "Gadget sources" && len(project.Checkouts) == 2
			}
		}
		return false
	})
}
