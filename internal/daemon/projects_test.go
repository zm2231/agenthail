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
	"github.com/zm2231/agenthail/internal/project"
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
	if len(state.Projects) != 1 || state.Projects[0].DisplayName != "widget" || state.Projects[0].RootPath != canonicalRepo || state.Projects[0].SessionCount != 2 || state.Projects[0].Archived {
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
		Project project.Project `json:"project"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &added); err != nil || added.Project.Kind != project.KindFolder || added.Project.DisplayName != "notes" {
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

	repoResponse := runDashboardAction(t, d, `{"action":"project-add","path":"`+fake.sessions["to"].Cwd+`"}`)
	var repoProject struct {
		Project project.Project `json:"project"`
	}
	if err := json.Unmarshal(repoResponse.Body.Bytes(), &repoProject); err != nil || repoProject.Project.Kind != project.KindGit || repoProject.Project.ID != storedProjection(t, r, "to").Project.ID {
		t.Fatalf("a repository folder was registered on its own: %s", repoResponse.Body)
	}
	if projects, err := r.ListProjects(); err != nil || len(projects) != 1 {
		t.Fatalf("projects=%+v err=%v", projects, err)
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
		Project project.Project `json:"project"`
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
		return project != nil && project.SessionCount == 2 && project.Host != nil && project.Host.ID == record.ID
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

func TestSnapshotNamesEachProjectsCoordinatorAndTheSessionItIs(t *testing.T) {
	d, r, _, repo := projectsFixture(t)
	other := filepath.Join(t.TempDir(), "unstarted")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	widget := state.Projects[0].ID
	if err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: widget, ProjectRoot: repo, Name: "widget-lead", Surface: "claude", Cwd: repo}, "to"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-unstarted", ProjectRoot: other, Name: "unstarted-lead", Surface: "codex", Cwd: other}, ""); err != nil {
		t.Fatal(err)
	}
	state, err = d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]dashboardProject{}
	for _, found := range state.Projects {
		byID[found.ID] = found
	}
	if lead := byID[widget].Coordinator; lead == nil || lead.Name != "widget-lead" || lead.SessionID != "to" || lead.Surface != "claude" {
		t.Fatalf("widget coordinator=%+v", lead)
	}
	unstarted := byID["project-unstarted"]
	if lead := unstarted.Coordinator; lead == nil || lead.Name != "unstarted-lead" || lead.SessionID != "" || unstarted.DisplayName != "unstarted" || unstarted.SessionCount != 0 {
		t.Fatalf("unstarted project=%+v coordinator=%+v", unstarted, lead)
	}
}
