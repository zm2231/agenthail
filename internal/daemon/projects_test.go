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
	"github.com/zm2231/agenthail/internal/workspace"
)

func projectsFixture(t *testing.T) (*Daemon, *registry.Registry, *daemonSurface, string) {
	t.Helper()
	d, r, fake, from, to := daemonFixture(t)
	repo := catalogTestRepo(t)
	worktree := filepath.Join(repo, ".worktrees", "task-c3")
	gitRun(t, repo, "worktree", "add", "-b", "feature/c3", worktree)
	idle := time.Now().Add(-30 * time.Hour).UTC().Truncate(time.Millisecond)
	from.Cwd, from.LastActive = repo, idle
	to.Cwd, to.LastActive = worktree, idle
	for _, session := range []surface.Session{from, to} {
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	return d, r, fake, repo
}

func runDashboardAction(t *testing.T, d *Daemon, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	d.dashboardActionHandler(response, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(body)))
	return response
}

func stateRow(t *testing.T, state dashboardState, id string) dashboardSession {
	t.Helper()
	for _, row := range state.Sessions {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("session %s is not in the snapshot", id)
	return dashboardSession{}
}

func storedProjection(t *testing.T, r *registry.Registry, id string) dashboardSession {
	t.Helper()
	raw, found, err := r.CatalogProjectionFingerprint(id)
	if err != nil || !found {
		t.Fatalf("projection for %s: found=%t err=%v", id, found, err)
	}
	var row dashboardSession
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestSnapshotGroupsWorktreeSessionsIntoOneProject(t *testing.T) {
	d, _, _, repo := projectsFixture(t)
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	from, to := stateRow(t, state, "from"), stateRow(t, state, "to")
	if from.Project == nil || to.Project == nil || from.Project.ID != to.Project.ID || to.Checkout.Branch != "feature/c3" {
		t.Fatalf("from=%+v to=%+v checkout=%+v", from.Project, to.Project, to.Checkout)
	}
	if len(state.Projects) != 1 || state.Projects[0].DisplayName != "widget" || len(state.Projects[0].Checkouts) != 1 || state.Projects[0].Checkouts[0].RootPath != canonicalRepo || state.Projects[0].SessionCount != 2 || state.Projects[0].Archived {
		t.Fatalf("projects=%+v", state.Projects)
	}
	if from.Current || state.Counts["recent"] != 0 || state.Counts["current"] != 0 || state.Counts["all"] != 2 || state.Counts["archived"] != 0 {
		t.Fatalf("from=%+v counts=%v", from, state.Counts)
	}
}

func TestArchivedSessionLeavesScopesUntilItIsActiveAgain(t *testing.T) {
	d, r, fake, _ := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"session-archive","sessionId":"from"}`); response.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", response.Code, response.Body)
	}
	if row := storedProjection(t, r, "from"); !row.Archived || row.Project.Archived {
		t.Fatalf("published row was not archived: %+v", row)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !stateRow(t, state, "from").Archived || state.Counts["all"] != 1 || state.Counts["recent"] != 0 || state.Counts["archived"] != 1 || state.Projects[0].SessionCount != 1 {
		t.Fatalf("counts=%v projects=%+v", state.Counts, state.Projects)
	}

	from := fake.sessions["from"]
	from.LastActive = time.Now().Add(time.Second).UTC()
	fake.sessions["from"] = from
	d.discoverCatalog(context.Background())
	if row := storedProjection(t, r, "from"); row.Archived {
		t.Fatalf("a new turn left the session archived: %+v", row)
	}
	archive, err := r.ArchiveState()
	if err != nil || len(archive.Sessions) != 0 {
		t.Fatalf("archive=%+v err=%v", archive, err)
	}
}

func TestQueuedWorkUnarchivesTheSessionAndItsProject(t *testing.T) {
	d, r, _, _ := projectsFixture(t)
	projectID := storedProjection(t, r, "to").Project.ID
	if response := runDashboardAction(t, d, `{"action":"project-archive","projectId":"`+projectID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", response.Code, response.Body)
	}
	if row := storedProjection(t, r, "to"); !row.Archived || !row.Project.Archived {
		t.Fatalf("project archive did not reach the row: %+v", row)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Counts["all"] != 0 || state.Counts["archived"] != 2 || !state.Projects[0].Archived {
		t.Fatalf("counts=%v projects=%+v", state.Counts, state.Projects)
	}

	if _, err := r.QueueMessageWithOptions("to", "follow up", "", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	d.publishCatalogPresence()
	to, from := storedProjection(t, r, "to"), storedProjection(t, r, "from")
	if to.Archived || to.Project.Archived || from.Archived {
		t.Fatalf("queued work stayed hidden: to=%+v from=%+v", to, from)
	}
	if archive, err := r.ArchiveState(); err != nil || len(archive.Projects) != 0 {
		t.Fatalf("archive=%+v err=%v", archive, err)
	}
}

func TestUnarchiveRestoresSessionAndProject(t *testing.T) {
	d, r, _, _ := projectsFixture(t)
	projectID := storedProjection(t, r, "from").Project.ID
	for _, body := range []string{
		`{"action":"session-archive","sessionId":"from"}`,
		`{"action":"session-unarchive","sessionId":"from"}`,
		`{"action":"project-archive","projectId":"` + projectID + `"}`,
		`{"action":"project-unarchive","projectId":"` + projectID + `"}`,
	} {
		if response := runDashboardAction(t, d, body); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, response.Code, response.Body)
		}
	}
	if row := storedProjection(t, r, "from"); row.Archived || row.Project.Archived {
		t.Fatalf("row=%+v", row)
	}
	for _, body := range []string{`{"action":"project-archive","projectId":"project-missing"}`, `{"action":"session-archive","sessionId":"missing"}`} {
		if response := runDashboardAction(t, d, body); response.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", body, response.Code, response.Body)
		}
	}
}

func TestAddingAFolderProjectClaimsItsSessions(t *testing.T) {
	d, r, fake, _ := projectsFixture(t)
	folder := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(filepath.Join(folder, "week-2"), 0700); err != nil {
		t.Fatal(err)
	}
	scratch := surface.Session{ID: "scratch", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: filepath.Join(folder, "week-2"), LastActive: time.Now().UTC()}
	fake.sessions[scratch.ID] = scratch
	if err := r.RegisterSession(scratch); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	if row := storedProjection(t, r, "scratch"); row.Project != nil {
		t.Fatalf("an unregistered folder became a project: %+v", row.Project)
	}

	response := runDashboardAction(t, d, `{"action":"project-add","path":"`+folder+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	var added struct {
		Project CatalogProject `json:"project"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &added); err != nil || added.Project.Kind != catalogProjectFolder || added.Project.DisplayName != "notes" {
		t.Fatalf("added=%+v err=%v", added, err)
	}
	select {
	case <-d.catalogNudge:
	default:
		t.Fatal("adding a project did not request discovery")
	}
	d.discoverCatalog(context.Background())
	if row := storedProjection(t, r, "scratch"); row.Project == nil || row.Project.ID != added.Project.ID {
		t.Fatalf("row=%+v", row.Project)
	}

	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+added.Project.ID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body)
	}
	d.discoverCatalog(context.Background())
	if row := storedProjection(t, r, "scratch"); row.Project != nil {
		t.Fatalf("a removed folder kept its sessions: %+v", row.Project)
	}
}

func TestAWorktreeSessionLandsInTheDeepestAddedProject(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	repo := catalogTestRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/example/merchantry.git")
	if err := os.MkdirAll(filepath.Join(repo, "apps", "web", "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web", "src", "index.ts"), []byte("export {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "apps")
	gitRun(t, repo, "commit", "-m", "web")
	worktree := filepath.Join(repo, ".worktrees", "task-f6")
	gitRun(t, repo, "worktree", "add", "-b", "feature/f6", worktree)
	for id, cwd := range map[string]string{"from": filepath.Join(worktree, "apps", "web", "src"), "to": worktree} {
		session := fake.sessions[id]
		session.Cwd = cwd
		fake.sessions[id] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	add := func(path string) *httptest.ResponseRecorder {
		return runDashboardAction(t, d, `{"action":"project-add","path":"`+path+`"}`)
	}
	if response := add(filepath.Join(repo, "apps")); response.Code != http.StatusOK {
		t.Fatalf("add apps: %d %s", response.Code, response.Body)
	}
	web := add(filepath.Join(worktree, "apps", "web"))
	var added struct {
		Project CatalogProject `json:"project"`
	}
	if web.Code != http.StatusOK || json.Unmarshal(web.Body.Bytes(), &added) != nil {
		t.Fatalf("add web: %d %s", web.Code, web.Body)
	}
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	if added.Project.RootPath != filepath.Join(canonicalRepo, "apps", "web") || added.Project.ID != "repo:github.com/example/merchantry::apps/web" || added.Project.Kind != catalogProjectGit || added.Project.DisplayName != "web" {
		t.Fatalf("a worktree folder was not added at the main checkout: %+v", added.Project)
	}
	again := add(filepath.Join(repo, "apps", "web"))
	var existing struct {
		Project CatalogProject `json:"project"`
		Created bool           `json:"created"`
	}
	if again.Code != http.StatusOK || json.Unmarshal(again.Body.Bytes(), &existing) != nil || existing.Created || existing.Project.LocalID != added.Project.LocalID || existing.Project.ID != added.Project.ID {
		t.Fatalf("duplicate add: %d %s", again.Code, again.Body)
	}

	d.discoverCatalog(context.Background())
	from, to := storedProjection(t, r, "from").Project, storedProjection(t, r, "to").Project
	if from == nil || from.ID != added.Project.ID || from.LocalID != added.Project.LocalID || !from.Added {
		t.Fatalf("from=%+v", from)
	}
	if to == nil || to.ID != "repo:github.com/example/merchantry" || to.Added || to.RootPath != canonicalRepo || to.DisplayName != "merchantry" {
		t.Fatalf("to=%+v", to)
	}

	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+added.Project.ID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body)
	}
	d.discoverCatalog(context.Background())
	if from := storedProjection(t, r, "from").Project; from == nil || from.ID != "repo:github.com/example/merchantry::apps" {
		t.Fatalf("after remove from=%+v", from)
	}
	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+to.ID+`"}`); response.Code != http.StatusConflict {
		t.Fatalf("removing an implicit project: %d %s", response.Code, response.Body)
	}
}

func TestScopeCountsCountFamiliesNotSubagents(t *testing.T) {
	now := time.Now()
	rows := []dashboardSession{
		{ID: "root", Status: surface.StatusIdle, LastActive: now.Add(-48 * time.Hour)},
		{ID: "child", Status: surface.StatusBusy, Current: true, Subagent: &surface.Subagent{ParentID: "root", RootID: "root"}},
		{ID: "orphan", Status: surface.StatusIdle, Current: true, Subagent: &surface.Subagent{ParentID: "gone", RootID: "gone"}},
		{ID: "hidden", Status: surface.StatusIdle, Current: true, Archived: true},
		{ID: "lead", Status: surface.StatusIdle, Current: true, Subagents: &surface.SubagentRollup{Count: 2, Working: 1}},
	}
	counts := catalogScopeCounts(rows)
	want := map[string]int{"running": 2, "current": 3, "recent": 3, "all": 3, "archived": 1}
	for scope, count := range want {
		if counts[scope] != count {
			t.Fatalf("counts=%v want=%v", counts, want)
		}
	}
}

func TestFederationProjectsStayOnTheirHostAndArchiveRoutesThere(t *testing.T) {
	local, remote, record := pairedFederation(t)
	cwd := remote.fake.sessions["remote-a"].Cwd
	added := remote.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-add", "path": cwd})
	var answer struct {
		Project CatalogProject `json:"project"`
	}
	if added.Code != http.StatusOK || json.Unmarshal(added.Body.Bytes(), &answer) != nil {
		t.Fatalf("add status=%d body=%s", added.Code, added.Body.String())
	}
	remote.daemon.discoverCatalog(context.Background())
	qualified := hosts.Qualify(record.ID, answer.Project.ID)
	mirroredProject := func() *dashboardProject {
		for _, project := range local.snapshot(t).Projects {
			if project.ID == qualified {
				return &project
			}
		}
		return nil
	}
	awaitFederation(t, "remote project mirrored", func() bool {
		project := mirroredProject()
		return project != nil && project.SessionCount == 2 && len(project.Checkouts) == 1 && project.Checkouts[0].Host.ID == record.ID && project.Checkouts[0].ProjectID == qualified
	})

	archived := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-archive", "projectId": qualified})
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `"projectId":"`+qualified+`"`) {
		t.Fatalf("archive status=%d body=%s", archived.Code, archived.Body.String())
	}
	if state, err := remote.reg.ArchiveState(); err != nil || state.Projects[answer.Project.ID].IsZero() {
		t.Fatalf("the owning host did not record the archive: %+v err=%v", state, err)
	}
	if state, err := local.reg.ArchiveState(); err != nil || len(state.Projects) != 0 {
		t.Fatalf("the archive was recorded on the wrong host: %+v err=%v", state, err)
	}
	awaitFederation(t, "mirrored rows archived", func() bool {
		project := mirroredProject()
		rows := sessionsOf(local.snapshot(t), record.ID)
		return project != nil && project.Archived && len(rows) == 2 && rows[0].Archived && rows[1].Archived
	})
}

func cloneAt(t *testing.T, origin, path string) string {
	t.Helper()
	gitRun(t, origin, "clone", "--quiet", origin, path)
	gitRun(t, path, "remote", "set-url", "origin", "https://github.com/example/gadget.git")
	if err := os.MkdirAll(filepath.Join(path, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func moveSessions(t *testing.T, node *federationNode, cwd string) {
	t.Helper()
	for id, session := range node.fake.sessions {
		session.Cwd = cwd
		node.fake.sessions[id] = session
		if err := node.reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	node.daemon.discoverCatalog(context.Background())
}

func TestFederationMergesOneRepositoryAcrossHostsAndArchivesItPerHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	remote := newFederationNode(t, "remote-a", "remote-b")
	moveSessions(t, remote, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
	remote.serve(t, nil)
	local := newFederationNode(t, "local-a")
	localRoot := cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget"))
	worktree := filepath.Join(localRoot, ".worktrees", "task-e5")
	gitRun(t, localRoot, "worktree", "add", "-b", "feature/e5", worktree)
	moveSessions(t, local, filepath.Join(worktree, "src"))
	local.start(t, testRoots(remote.server))
	record := local.pair(t, remote, "vps")
	for _, node := range []*federationNode{local, remote} {
		folder := map[*federationNode]string{local: filepath.Join(localRoot, "src"), remote: remote.fake.sessions["remote-a"].Cwd}[node]
		if response := node.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-add", "path": folder}); response.Code != http.StatusOK {
			t.Fatalf("add: %d %s", response.Code, response.Body)
		}
		node.daemon.discoverCatalog(context.Background())
	}

	projectID := storedProjection(t, local.reg, "local-a").Project.ID
	if projectID != "repo:github.com/example/gadget::src" {
		t.Fatalf("project id=%q", projectID)
	}
	merged := func() []dashboardProject {
		var found []dashboardProject
		for _, project := range local.snapshot(t).Projects {
			if project.ID == projectID || project.ID == hosts.Qualify(record.ID, projectID) {
				found = append(found, project)
			}
		}
		return found
	}
	awaitFederation(t, "one project with both checkouts", func() bool {
		projects := merged()
		return len(projects) == 1 && projects[0].ID == projectID && len(projects[0].Checkouts) == 2 && projects[0].SessionCount == 3
	})
	project := merged()[0]
	remoteLocalID := storedProjection(t, remote.reg, "remote-a").Project.LocalID
	if project.DisplayName != "src" || project.Key != projectID || !project.Checkouts[0].Added || project.Checkouts[0].Host.State != hostStateLocal || project.Checkouts[1].Host.ID != record.ID || project.Checkouts[0].SessionCount != 1 || project.Checkouts[1].SessionCount != 2 {
		t.Fatalf("project=%+v", project)
	}

	if response := remote.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-archive", "projectId": projectID}); response.Code != http.StatusOK {
		t.Fatalf("archive on vps: %d %s", response.Code, response.Body)
	}
	awaitFederation(t, "vps checkout archived", func() bool {
		projects := merged()
		return len(projects) == 1 && projects[0].Checkouts[1].Archived && !projects[0].Checkouts[0].Archived && !projects[0].Archived
	})

	if response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-unarchive", "projectId": projectID}); response.Code != http.StatusOK {
		t.Fatalf("unarchive everywhere: %d %s", response.Code, response.Body)
	}
	if state, err := remote.reg.ArchiveState(); err != nil || len(state.Projects) != 0 {
		t.Fatalf("vps kept the archive: %+v err=%v", state, err)
	}
	archived := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "project-archive", "projectId": projectID})
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), `"host":"vps","ok":true`) {
		t.Fatalf("archive everywhere: %d %s", archived.Code, archived.Body)
	}
	awaitFederation(t, "project archived on both hosts", func() bool {
		projects := merged()
		return len(projects) == 1 && projects[0].Archived && projects[0].Checkouts[0].Archived && projects[0].Checkouts[1].Archived
	})
	if state, err := remote.reg.ArchiveState(); err != nil || len(state.Projects) != 1 || state.Projects[remoteLocalID].IsZero() {
		t.Fatalf("vps did not record the archive: %+v err=%v", state, err)
	}
}

func TestTheCatalogAndPathResolutionAgreeRightAfterOriginChanges(t *testing.T) {
	d, r, fake, repo := projectsFixture(t)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/example/widget.git")
	d.discoverCatalog(context.Background())
	if id := storedProjection(t, r, "from").Project.ID; id != "repo:github.com/example/widget" {
		t.Fatalf("before: %s", id)
	}
	gitRun(t, repo, "remote", "set-url", "origin", "git@github.com:another-owner/widget-renamed.git")
	d.discoverCatalog(context.Background())
	resolved, err := ResolvePathProject(context.Background(), r, repo)
	if err != nil {
		t.Fatal(err)
	}
	if stored := storedProjection(t, r, "from").Project.ID; stored != "repo:github.com/another-owner/widget-renamed" || resolved == nil || resolved.ID != stored {
		t.Fatalf("catalog=%s resolved=%+v", stored, resolved)
	}

	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+filepath.Join(repo, "src")+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	from := fake.sessions["from"]
	from.Cwd = filepath.Join(repo, "src", "lib")
	fake.sessions["from"] = from
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	if id := storedProjection(t, r, "from").Project.ID; id != "repo:github.com/another-owner/widget-renamed::src" {
		t.Fatalf("added project before: %s", id)
	}
	gitRun(t, repo, "remote", "set-url", "origin", "https://github.com/example/widget.git")
	d.discoverCatalog(context.Background())
	records, err := r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if id := storedProjection(t, r, "from").Project.ID; id != "repo:github.com/example/widget::src" || len(records) != 1 || records[0].Key != id {
		t.Fatalf("added project after: session=%s records=%+v", id, records)
	}
}

func TestEveryProjectSurfaceNamesItsCoordinatorsOnEveryHost(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	repo, err := filepath.EvalSymlinks(catalogTestRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/example/widget.git")
	from.Cwd, to.Cwd = repo, repo
	for _, session := range []surface.Session{from, to} {
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	call := func(method, path, body string, into any) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body)
		}
		if into != nil {
			if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
				t.Fatal(err)
			}
		}
	}
	projects := func() map[string]dashboardProject {
		t.Helper()
		var state dashboardState
		call(http.MethodGet, "/api/v1/snapshot?fresh=1", "", &state)
		byID := map[string]dashboardProject{}
		for _, found := range state.Projects {
			byID[found.ID] = found
		}
		return byID
	}
	launchable := func(id string) bool {
		t.Helper()
		var options struct {
			Projects []launchProject `json:"projects"`
		}
		call(http.MethodGet, "/api/v1/session-options", "", &options)
		for _, found := range options.Projects {
			if found.ID == id {
				return true
			}
		}
		return false
	}
	widget, found := projects()["repo:github.com/example/widget"]
	if !found {
		t.Fatalf("projects=%+v", projects())
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: widget.ID, ProjectRoot: repo, Name: "widget-lead", Surface: "claude", Cwd: repo}, registry.CoordinatorChange{SessionID: to.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	unstartedRoot := filepath.Join(t.TempDir(), "unstarted")
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "repo:github.com/example/unstarted", ProjectRoot: unstartedRoot, Name: "unstarted-lead", Surface: "codex", Cwd: unstartedRoot}, registry.CoordinatorChange{ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	byID := projects()
	if leads := byID[widget.ID].Coordinators; len(leads) != 1 || leads[0].Name != "widget-lead" || leads[0].SessionID != to.ID || leads[0].Host.State != hostStateLocal || leads[0].ChangedBy != "fixture" {
		t.Fatalf("widget coordinators=%+v", leads)
	}
	unstarted := byID["repo:github.com/example/unstarted"]
	if leads := unstarted.Coordinators; len(leads) != 1 || leads[0].SessionID != "" || unstarted.DisplayName != "unstarted" || unstarted.SessionCount != 0 || unstarted.Archived || len(unstarted.Checkouts) != 1 || unstarted.Checkouts[0].RootPath != unstartedRoot || unstarted.Checkouts[0].Host.State != hostStateLocal {
		t.Fatalf("unstarted project=%+v", unstarted)
	}
	localID := unstarted.Checkouts[0].ProjectID
	if !launchable(localID) {
		t.Fatal("a coordinator's project without sessions is not offered for a new session")
	}
	call(http.MethodPost, "/api/v1/actions", `{"action":"project-archive","projectId":"repo:github.com/example/unstarted"}`, nil)
	if found := projects()["repo:github.com/example/unstarted"]; !found.Archived || len(found.Coordinators) != 1 {
		t.Fatalf("archived project=%+v", found)
	}
	if launchable(localID) {
		t.Fatal("an archived project is offered for a new session")
	}
	call(http.MethodPost, "/api/v1/actions", `{"action":"project-unarchive","projectId":"repo:github.com/example/unstarted"}`, nil)
	if found := projects()["repo:github.com/example/unstarted"]; found.Archived || !launchable(localID) {
		t.Fatalf("unarchived project=%+v", found)
	}

	if err := r.PutHostCoordinator("vps", registry.CoordinatorEntry{ProjectID: widget.ID, ProjectRoot: "/srv/widget", Name: "vps-lead", Surface: "codex", SessionID: hosts.Qualify("vps", "s-vps"), ChangedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if leads := projects()[widget.ID].Coordinators; len(leads) != 2 || leads[0].Name != "widget-lead" || leads[1].Name != "vps-lead" || leads[1].Host.ID != "vps" || leads[1].SessionID != hosts.Qualify("vps", "s-vps") {
		t.Fatalf("conflicting coordinators=%+v", leads)
	}

	gitRun(t, repo, "remote", "set-url", "origin", "https://github.com/example/widget-renamed.git")
	d.discoverCatalog(context.Background())
	byID = projects()
	renamed, found := byID["repo:github.com/example/widget-renamed"]
	if _, stale := byID[widget.ID]; stale || !found || len(renamed.Coordinators) != 0 || len(renamed.Checkouts) != 1 {
		t.Fatalf("after the origin changed: projects=%+v", byID)
	}
	var options struct {
		Projects []launchProject `json:"projects"`
	}
	call(http.MethodGet, "/api/v1/session-options", "", &options)
	offered := 0
	for _, found := range options.Projects {
		if found.RootPath == repo {
			offered++
		}
	}
	if offered != 1 {
		t.Fatalf("session options=%+v", options.Projects)
	}
}

func TestAnAddedProjectsCoordinatorStaysOneProjectAfterTheOriginChanges(t *testing.T) {
	d, r, fake, repo := projectsFixture(t)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/example/widget.git")
	// The project is reached through a symlink, as every macOS temp path is
	// through /var.
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(repo, linked); err != nil {
		t.Fatal(err)
	}
	from := fake.sessions["from"]
	from.Cwd = linked
	fake.sessions[from.ID] = from
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+linked+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	d.discoverCatalog(context.Background())
	records, err := r.ListProjects()
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	added := records[0]
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"widget-lead"}`); response.Code != http.StatusOK {
		t.Fatalf("set coordinator: %d %s", response.Code, response.Body)
	}
	gitRun(t, repo, "remote", "set-url", "origin", "https://github.com/example/widget-renamed.git")
	d.discoverCatalog(context.Background())
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	atRoot := 0
	for _, project := range state.Projects {
		for _, at := range project.Checkouts {
			if at.RootPath == added.WorkspaceRoot {
				atRoot++
			}
		}
	}
	if atRoot != 1 {
		t.Fatalf("projects=%+v", state.Projects)
	}
	options, err := d.launchProjects()
	if err != nil {
		t.Fatal(err)
	}
	offered := 0
	for _, found := range options {
		if found.RootPath == added.WorkspaceRoot {
			offered++
			if found.ID != added.ID {
				t.Fatalf("offered=%+v added=%+v", found, added)
			}
		}
	}
	if offered != 1 {
		t.Fatalf("session options=%+v", options)
	}
	response := runDashboardAction(t, d, `{"action":"project-archive","projectId":"`+added.Key+`"}`)
	want := "project " + added.Key + " is not listed on " + d.hostIdentity().Name + "; it may be listed under another id now, for example after its repository's origin changed; refresh the project list and try again"
	if response.Code != http.StatusNotFound || apiErrorCode(t, response) != "project_not_found" || !strings.Contains(response.Body.String(), want) {
		t.Fatalf("archive by the id the project no longer has: %d %q", response.Code, response.Body)
	}
	archive, err := r.ArchiveState()
	if err != nil || len(archive.Projects) != 0 {
		t.Fatalf("archived=%v err=%v", archive.Projects, err)
	}
}
