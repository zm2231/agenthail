package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

const merchantryKey = "repo:github.com/example/merchantry"

type worktreeProjectFixture struct {
	d                        *Daemon
	r                        *registry.Registry
	fake                     *daemonSurface
	repo, v2, worker, nested string
}

func newWorktreeProjectFixture(t *testing.T) worktreeProjectFixture {
	t.Helper()
	d, r, fake, _, _ := daemonFixture(t)
	repo := testkit.GitRepo(t)
	testkit.Git(t, repo, "remote", "add", "origin", "https://github.com/example/merchantry.git")
	if err := os.MkdirAll(filepath.Join(repo, "apps"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "apps", "web.ts"), []byte("export {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, repo, "add", "apps")
	testkit.Git(t, repo, "commit", "-m", "apps")
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	f := worktreeProjectFixture{d: d, r: r, fake: fake, repo: repo, v2: filepath.Join(filepath.Dir(repo), "merchantry-v2")}
	testkit.Git(t, repo, "worktree", "add", "-b", "v2/cockpit", f.v2)
	f.worker = filepath.Join(repo, ".worktrees", "fix-a")
	testkit.Git(t, repo, "worktree", "add", "-b", "fix/a", f.worker)
	f.nested = filepath.Join(f.v2, ".worktrees", "v2-b")
	testkit.Git(t, f.v2, "worktree", "add", "-b", "v2/b", f.nested)
	for id, cwd := range map[string]string{"main": repo, "v2": filepath.Join(f.v2, "apps"), "worker": f.worker, "nested": f.nested} {
		f.session(t, id, cwd)
	}
	delete(fake.sessions, "from")
	delete(fake.sessions, "to")
	return f
}

func (f worktreeProjectFixture) session(t *testing.T, id, cwd string) {
	t.Helper()
	session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: cwd}
	f.fake.sessions[id] = session
	if err := f.r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
}

func (f worktreeProjectFixture) add(t *testing.T, path string) projectresolve.CatalogProject {
	t.Helper()
	response := runDashboardAction(t, f.d, `{"action":"project-add","path":"`+path+`"}`)
	var answer struct {
		Project projectresolve.CatalogProject `json:"project"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("project-add %s: %d %s", path, response.Code, response.Body)
	}
	return answer.Project
}

func (f worktreeProjectFixture) projectOf(t *testing.T, id string) string {
	t.Helper()
	f.d.discoverCatalog(context.Background())
	project := storedProjection(t, f.r, id).Project
	if project == nil {
		t.Fatalf("session %s has no project", id)
	}
	return project.ID
}

func (f worktreeProjectFixture) lead(t *testing.T, sessionID, name string) {
	t.Helper()
	if response := runDashboardAction(t, f.d, `{"action":"coordinator-set","sessionId":"`+sessionID+`","name":"`+name+`"}`); response.Code != http.StatusOK {
		t.Fatalf("coordinator-set %s: %d %s", name, response.Code, response.Body)
	}
}

func TestAnAddedWorktreeIsAProjectOfItsOwnWhileWorktreesNobodyAddedStayInTheRepository(t *testing.T) {
	f := newWorktreeProjectFixture(t)
	f.lead(t, "main", "captain")
	for _, id := range []string{"main", "v2", "worker", "nested"} {
		if got := f.projectOf(t, id); got != merchantryKey {
			t.Fatalf("before the add, %s is in %s, want the repository's project", id, got)
		}
	}

	v2 := f.add(t, f.v2)
	if !strings.HasPrefix(v2.ID, "p-") || v2.Key != "" || v2.RootPath != f.v2 || v2.Kind != projectresolve.CatalogProjectGit || v2.DisplayName != "merchantry-v2" {
		t.Fatalf("the added worktree is %+v, want a keyless project at %s", v2, f.v2)
	}
	if again := f.add(t, f.v2); again.ID != v2.ID {
		t.Fatalf("adding the worktree again answered %+v, want %s", again, v2.ID)
	}
	apps := f.add(t, filepath.Join(f.repo, "apps"))
	for id, want := range map[string]string{"main": merchantryKey, "v2": v2.ID, "nested": v2.ID, "worker": merchantryKey} {
		if got := f.projectOf(t, id); got != want {
			t.Fatalf("%s is in %s, want %s", id, got, want)
		}
	}
	f.session(t, "worker-apps", filepath.Join(f.worker, "apps"))
	if got := f.projectOf(t, "worker-apps"); got != apps.ID {
		t.Fatalf("a worker in apps is in %s, want the main checkout's apps project %s", got, apps.ID)
	}

	if key := ledgerKeyAt(t, f.r, filepath.Join(f.v2, "apps")); key != registry.LedgerProjectKey(v2.ID) {
		t.Fatalf("the worktree project's ledger is %s, want its own", key)
	}
	if key := ledgerKeyAt(t, f.r, f.nested); key != registry.LedgerProjectKey(v2.ID) {
		t.Fatalf("a worktree nested in the worktree project names ledger %s, want the worktree project's", key)
	}
	if key := ledgerKeyAt(t, f.r, f.worker); key != merchantryKey {
		t.Fatalf("a worker worktree names ledger %s, want the repository's", key)
	}
	if lead, err := f.r.Coordinator(merchantryKey); err != nil || lead.Name != "captain" {
		t.Fatalf("after the add the repository's lead is %+v (%v), want @captain", lead, err)
	}
	if lead, err := f.r.Coordinator(v2.ID); err == nil {
		t.Fatalf("the new worktree project took a lead: %+v", lead)
	}
	f.lead(t, "v2", "cockpit")
	if lead, err := f.r.Coordinator(v2.ID); err != nil || lead.Name != "cockpit" {
		t.Fatalf("the worktree project's lead is %+v (%v), want @cockpit", lead, err)
	}
}

func TestRemovingAWorktreeProjectReturnsItsSessionsAndAddingItAgainBringsBackItsLead(t *testing.T) {
	f := newWorktreeProjectFixture(t)
	f.lead(t, "main", "captain")
	v2 := f.add(t, f.v2)
	if got := f.projectOf(t, "v2"); got != v2.ID {
		t.Fatalf("v2 is in %s", got)
	}
	f.lead(t, "v2", "cockpit")

	if response := runDashboardAction(t, f.d, `{"action":"project-remove","projectId":"`+v2.ID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("removing the worktree project while both projects have a lead: %d %s", response.Code, response.Body)
	}
	for _, id := range []string{"v2", "nested"} {
		if got := f.projectOf(t, id); got != merchantryKey {
			t.Fatalf("after the remove %s is in %s, want the repository's project", id, got)
		}
	}
	if lead, err := f.r.Coordinator(merchantryKey); err != nil || lead.Name != "captain" {
		t.Fatalf("after the remove the repository's lead is %+v (%v), want @captain", lead, err)
	}
	if lead, err := f.r.Coordinator(v2.ID); err != nil || lead.Name != "cockpit" {
		t.Fatalf("after the remove the worktree project's lead is %+v (%v), want @cockpit kept under its id", lead, err)
	}

	if again := f.add(t, f.v2); again.ID != v2.ID {
		t.Fatalf("adding the worktree again made %s, want %s back with its lead", again.ID, v2.ID)
	}
	if got := f.projectOf(t, "v2"); got != v2.ID {
		t.Fatalf("after the re-add v2 is in %s", got)
	}
}

func TestAWorktreeInsideTheRepositoryAddedAsAProjectNeverGainsAKey(t *testing.T) {
	f := newWorktreeProjectFixture(t)
	worker := f.add(t, f.worker)
	f.d.discoverCatalog(context.Background())
	f.d.discoverCatalog(context.Background())
	if got := f.projectOf(t, "worker"); got != worker.ID || !strings.HasPrefix(got, "p-") {
		t.Fatalf("the added worker worktree is %s, want its own keyless project %s", got, worker.ID)
	}
	records, err := f.r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.ID == worker.ID && (record.Key != "" || !record.Worktree) {
			t.Fatalf("the worktree project's record is %+v, want a worktree without a key", record)
		}
	}
}

func TestABareRepositorysWorktreeIsAddedAsTheRepository(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	source := testkit.GitRepo(t)
	bare := filepath.Join(t.TempDir(), "app.git")
	testkit.Git(t, source, "clone", "--quiet", "--bare", source, bare)
	one, two := filepath.Join(filepath.Dir(bare), "one"), filepath.Join(filepath.Dir(bare), "two")
	testkit.Git(t, bare, "worktree", "add", "-b", "one", one)
	testkit.Git(t, bare, "worktree", "add", "-b", "two", two)
	first := addFolderProject(t, d, one)
	second := addFolderProject(t, d, two)
	if first != second {
		t.Fatalf("the two worktrees of a bare repository became projects %s and %s, want one", first, second)
	}
}

func TestASessionStartedWithItsOwnWorktreeInAWorktreeProjectStaysInIt(t *testing.T) {
	f := newWorktreeProjectFixture(t)
	v2 := f.add(t, f.v2)
	code, answer := workAction(t, f.d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": filepath.Join(f.v2, "apps"), "isolation": "worktree", "alias": "builder"})
	if code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	path := filepath.Join(f.v2, ".worktrees", "ah-builder")
	if made, _ := answer["worktree"].(map[string]any); made["path"] != path {
		t.Fatalf("the worktree was made at %v, want %s", answer["worktree"], path)
	}
	if work := workOf(t, f.r, "started"); work.RepoRoot != f.v2 {
		t.Fatalf("the work is recorded in %s, want the worktree project %s", work.RepoRoot, f.v2)
	}
	f.session(t, "builder", filepath.Join(path, "apps"))
	if got := f.projectOf(t, "builder"); got != v2.ID {
		t.Fatalf("the builder is in %s, want the worktree project %s", got, v2.ID)
	}
}

func TestAutomaticCleanupKeepsAWorktreeAddedAsAProject(t *testing.T) {
	f := newWorktreeProjectFixture(t)
	code, answer := workAction(t, f.d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": f.repo, "isolation": "worktree", "alias": "keeper"})
	if code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	path := filepath.Join(f.repo, ".worktrees", "ah-keeper")
	f.add(t, path)
	if code, answer := workAction(t, f.d, map[string]any{"action": "session-archive", "sessionId": "started"}); code != http.StatusOK {
		t.Fatal(code, answer)
	}
	settleIdle(t, f.r, "started")
	f.d.sweepWork(context.Background(), time.Now())
	if work := workOf(t, f.r, "started"); work.CleanupReason != registry.WorkKeptProject {
		t.Fatalf("work in an added project's folder is %+v, want it kept as a project", work)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the added project's folder was removed: %v", err)
	}
}
