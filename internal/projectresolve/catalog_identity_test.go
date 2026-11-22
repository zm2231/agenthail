package projectresolve

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
	"github.com/zm2231/agenthail/internal/workspace"
)

func noStoredProject() *CatalogProject { return nil }

func TestCatalogIdentityMapsWorktreesAndSubfoldersToTheMainCheckout(t *testing.T) {
	repo := testkit.GitRepo(t)
	child := filepath.Join(repo, ".worktrees", "task-a1")
	testkit.Git(t, repo, "worktree", "add", "-b", "feature/catalog", child)
	if err := os.WriteFile(filepath.Join(child, "README.md"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cache := NewCatalogIdentityCache()
	base := cache.Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
	worktree := cache.Identity(context.Background(), surface.Session{Cwd: child}, time.Now(), noStoredProject)
	nested := cache.Identity(context.Background(), surface.Session{Cwd: filepath.Join(repo, "src", "lib")}, time.Now(), noStoredProject)
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	canonicalChild, err := workspace.NormalizeCWD(child)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []CatalogIdentity{base, worktree, nested} {
		if identity.Problem != "" || identity.Project == nil {
			t.Fatalf("identity=%+v", identity)
		}
		if identity.Project.ID != base.Project.ID || identity.Project.DisplayName != "widget" || identity.Project.RootPath != canonicalRepo || identity.Project.Kind != CatalogProjectGit {
			t.Fatalf("project=%+v base=%+v", identity.Project, base.Project)
		}
	}
	if base.Checkout.ID == worktree.Checkout.ID || base.Checkout.Path != canonicalRepo || worktree.Checkout.Path != canonicalChild || nested.Checkout.Path != canonicalRepo {
		t.Fatalf("base=%+v worktree=%+v nested=%+v", base.Checkout, worktree.Checkout, nested.Checkout)
	}
	if !base.Checkout.IsMain || worktree.Checkout.Branch != "feature/catalog" || !worktree.Checkout.Dirty {
		t.Fatalf("base=%+v worktree=%+v", base.Checkout, worktree.Checkout)
	}
}

func TestCatalogIdentityKeepsARemovedWorktreeInItsProject(t *testing.T) {
	repo := testkit.GitRepo(t)
	child := filepath.Join(repo, ".worktrees", "task-b2")
	testkit.Git(t, repo, "worktree", "add", "-b", "feature/removed", child)
	testkit.Git(t, repo, "worktree", "remove", child)
	identity := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: child}, time.Now(), noStoredProject)
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Problem != "" || identity.Project == nil || identity.Project.RootPath != canonicalRepo || identity.Checkout.Path != filepath.Join(canonicalRepo, ".worktrees", "task-b2") {
		t.Fatalf("identity=%+v project=%+v", identity, identity.Project)
	}
	gone := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: filepath.Join(t.TempDir(), "scratch", "gone")}, time.Now(), noStoredProject)
	if gone.Project != nil || gone.Problem != "" {
		t.Fatalf("gone=%+v", gone)
	}
}

func TestCatalogIdentityLeavesUnregisteredFoldersOutOfProjects(t *testing.T) {
	path := t.TempDir()
	identity := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: path}, time.Now(), noStoredProject)
	canonicalPath, err := workspace.NormalizeCWD(path)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Problem != "" || identity.Project != nil || identity.Checkout.Path != canonicalPath {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestCatalogIdentityUsesTheDeepestAddedProject(t *testing.T) {
	root, err := workspace.NormalizeCWD(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(root, "notes")
	inner := filepath.Join(outer, "drafts")
	work := filepath.Join(inner, "week-1")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	cache := NewCatalogIdentityCache()
	cache.SetProjects([]registry.ProjectRecord{
		{ID: "p-outer", WorkspaceRoot: outer, Title: "notes", Added: true},
		{ID: "p-inner", WorkspaceRoot: inner, Title: "drafts", Added: true},
		{ID: "implicit:" + root, WorkspaceRoot: root, Title: "root"},
	})
	identity := cache.Identity(context.Background(), surface.Session{Cwd: work}, time.Now(), noStoredProject)
	if identity.Project == nil || identity.Project.ID != "p-inner" || identity.Project.LocalID != "p-inner" || !identity.Project.Added || identity.Project.Kind != CatalogProjectFolder || identity.Project.RootPath != inner {
		t.Fatalf("identity=%+v", identity.Project)
	}
	if outside := cache.Identity(context.Background(), surface.Session{Cwd: root}, time.Now(), noStoredProject); outside.Project != nil {
		t.Fatalf("outside=%+v", outside.Project)
	}
}

func TestCatalogIdentityKeepsTheKnownProjectWhenGitFails(t *testing.T) {
	repo := testkit.GitRepo(t)
	cache := NewCatalogIdentityCache()
	resolved := cache.Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
	if resolved.Project == nil {
		t.Fatalf("resolved=%+v", resolved)
	}
	t.Setenv("PATH", t.TempDir())
	later := time.Now().Add(catalogIdentityTTL)
	cache.Identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	cache.Wait()
	failed := cache.Identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	if failed.Problem == "" || failed.Project == nil || failed.Project.ID != resolved.Project.ID {
		t.Fatalf("failed=%+v resolved=%+v", failed, resolved.Project)
	}
	stored := &CatalogProject{ID: "project-stored", DisplayName: "widget", RootPath: repo, Kind: CatalogProjectGit}
	fresh := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: repo}, later, func() *CatalogProject { return stored })
	if fresh.Problem == "" || fresh.Project != stored {
		t.Fatalf("fresh=%+v", fresh)
	}
}

func TestCatalogIdentityKeepsTheProjectWhenGitRefusesTheRepository(t *testing.T) {
	repo := testkit.GitRepo(t)
	cache := NewCatalogIdentityCache()
	resolved := cache.Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
	if resolved.Project == nil {
		t.Fatalf("resolved=%+v", resolved)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: detected dubious ownership in repository' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	later := time.Now().Add(catalogIdentityTTL)
	cache.Identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	cache.Wait()
	refused := cache.Identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	if refused.Problem == "" || refused.Project == nil || refused.Project.ID != resolved.Project.ID {
		t.Fatalf("refused=%+v resolved=%+v", refused, resolved.Project)
	}
}

func catalogProjectAt(t *testing.T, cache *CatalogIdentityCache, path string) *CatalogProject {
	t.Helper()
	identity := cache.Identity(context.Background(), surface.Session{Cwd: path}, time.Now(), noStoredProject)
	if identity.Problem != "" || identity.Project == nil {
		t.Fatalf("identity for %s: %+v", path, identity)
	}
	return identity.Project
}

func TestImplicitProjectsShareARepositoryOnlyThroughItsRemote(t *testing.T) {
	first := testkit.GitRepo(t)
	testkit.Git(t, first, "remote", "add", "origin", "https://github.com/Example/Gadget.git")
	testkit.Git(t, first, "remote", "add", "upstream", "https://github.com/upstream-org/gadget.git")
	second := filepath.Join(t.TempDir(), "checkout-two")
	testkit.Git(t, first, "clone", "--quiet", first, second)
	testkit.Git(t, second, "remote", "set-url", "origin", "git@github.com:example/gadget")
	alone := filepath.Join(t.TempDir(), "alone")
	testkit.Git(t, first, "clone", "--quiet", first, alone)
	testkit.Git(t, alone, "remote", "remove", "origin")

	cache := NewCatalogIdentityCache()
	one := catalogProjectAt(t, cache, filepath.Join(first, "src", "lib"))
	two := catalogProjectAt(t, cache, second)
	if one.ID != "repo:github.com/example/gadget" || two.ID != one.ID || one.LocalID == two.LocalID || one.DisplayName != "gadget" || one.Added || one.Repository == nil || one.Repository.RemoteName != "origin" {
		t.Fatalf("one=%+v two=%+v", one, two)
	}
	canonicalAlone, err := workspace.NormalizeCWD(alone)
	if err != nil {
		t.Fatal(err)
	}
	local := catalogProjectAt(t, cache, alone)
	if local.Key != "" || local.ID != "implicit:"+canonicalAlone || local.LocalID != local.ID || local.Repository != nil || local.DisplayName != "alone" {
		t.Fatalf("a repository without a remote: %+v", local)
	}
}

func TestImplicitProjectsCoverBareRepositoriesAndDottedRemoteNames(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "mirror.git")
	if err := os.MkdirAll(bare, 0700); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, bare, "init", "--bare")
	testkit.Git(t, bare, "remote", "add", "team.backup", "/srv/git/mirror.git")
	testkit.Git(t, bare, "remote", "add", "team.main", "https://github.com/example/mirror.git")
	testkit.Git(t, bare, "config", "--add", "remote.team.main.url", "https://github.com/example/mirror-second.git")
	canonicalBare, err := workspace.NormalizeCWD(bare)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCatalogIdentityCache()
	found := catalogProjectAt(t, cache, filepath.Join(bare, "refs"))
	if found.ID != "repo:github.com/example/mirror" || found.RootPath != canonicalBare || found.DisplayName != "mirror" || found.Repository == nil || found.Repository.RemoteName != "team.main" {
		t.Fatalf("bare repository: %+v repository=%+v", found, found.Repository)
	}
}

func TestChangingOriginMovesTheProject(t *testing.T) {
	repo := testkit.GitRepo(t)
	testkit.Git(t, repo, "remote", "add", "origin", "https://github.com/example/gadget.git")
	cache := NewCatalogIdentityCache()
	before := catalogProjectAt(t, cache, repo)
	testkit.Git(t, repo, "remote", "set-url", "origin", "https://github.com/someone-else/gadget.git")
	later := cache.Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
	if before.ID != "repo:github.com/example/gadget" || later.Project == nil || later.Project.ID != "repo:github.com/someone-else/gadget" {
		t.Fatalf("before=%+v later=%+v", before, later.Project)
	}
}

// gateGit puts a git on PATH that waits for the returned release before
// running the real one.
func gateGit(t *testing.T) (release func()) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	gate := filepath.Join(bin, "open")
	script := fmt.Sprintf("#!/bin/sh\nwhile [ ! -f %q ]; do sleep 0.02; done\nexec %q \"$@\"\n", gate, real)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() {
		if err := os.WriteFile(gate, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExpiredIdentitiesRefreshInTheBackgroundOnePerPathWithinTheBound(t *testing.T) {
	cache := NewCatalogIdentityCache()
	now := time.Now()
	var repos []string
	for range catalogIdentityRefreshes + 2 {
		repo := testkit.GitRepo(t)
		testkit.SettleGitFiles(t, repo)
		repos = append(repos, repo)
		cache.Identity(context.Background(), surface.Session{Cwd: repo}, now, noStoredProject)
	}
	release := gateGit(t)
	defer cache.Wait()
	defer release()
	later := now.Add(catalogIdentityTTL)
	started := time.Now()
	for range 2 {
		for _, repo := range repos {
			if identity := cache.Identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject); identity.Project == nil {
				t.Fatalf("expired identity was not served: %+v", identity)
			}
		}
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lookups waited on git for %s", elapsed)
	}
	cache.mu.Lock()
	refreshing := len(cache.refreshing)
	cache.mu.Unlock()
	if refreshing != catalogIdentityRefreshes {
		t.Fatalf("refreshing=%d want %d", refreshing, catalogIdentityRefreshes)
	}
	release()
	cache.Wait()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	refreshed := 0
	for _, repo := range repos {
		path, _ := catalogSessionPath(repo)
		if cache.entries[path].resolvedAt.Equal(later) {
			refreshed++
		}
	}
	if refreshed != catalogIdentityRefreshes || len(cache.refreshing) != 0 || len(cache.slots) != 0 {
		t.Fatalf("refreshed=%d refreshing=%d slots=%d", refreshed, len(cache.refreshing), len(cache.slots))
	}
}

func TestAWorkspaceWhoseGitFailsIsNotReadAgainBeforeItExpires(t *testing.T) {
	repo := testkit.GitRepo(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\necho 'fatal: detected dubious ownership in repository' >&2\nexit 128\n", log)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	calls := func() int {
		raw, _ := os.ReadFile(log)
		return len(raw) / 2
	}
	cache := NewCatalogIdentityCache()
	now := time.Now()
	failed := cache.Identity(context.Background(), surface.Session{Cwd: repo}, now, noStoredProject)
	if failed.Problem == "" {
		t.Fatalf("failed=%+v", failed)
	}
	before := calls()
	if again := cache.Identity(context.Background(), surface.Session{Cwd: repo}, now.Add(time.Minute), noStoredProject); again.Problem == "" || calls() != before {
		t.Fatalf("failed workspace read again: %d git calls", calls()-before)
	}
}

func TestAnUntrackedFileInANewFolderMakesTheCheckoutDirty(t *testing.T) {
	repo := testkit.GitRepo(t)
	if clean := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject); clean.Checkout.Dirty {
		t.Fatalf("clean checkout reads dirty: %+v", clean.Checkout)
	}
	if err := os.MkdirAll(filepath.Join(repo, "notes", "drafts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes", "drafts", "idea.md"), []byte("idea\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if dirty := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject); !dirty.Checkout.Dirty {
		t.Fatalf("untracked file did not make the checkout dirty: %+v", dirty.Checkout)
	}
}

// inOneTick is a modification time a later read still counts as inside the
// tick of its change, however slowly the test runs.
func inOneTick() time.Time { return time.Now().Add(time.Minute) }

func setRemoteKeepingTime(t *testing.T, repo, url string, at time.Time) {
	t.Helper()
	testkit.Git(t, repo, "remote", "set-url", "origin", url)
	config := filepath.Join(repo, ".git", "config")
	if err := os.Chtimes(config, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestARemoteRewrittenAtTheSameSizeInOneTickMovesTheProject(t *testing.T) {
	repo := testkit.GitRepo(t)
	testkit.Git(t, repo, "remote", "add", "origin", "https://github.com/example/gadget.git")
	tick := inOneTick()
	setRemoteKeepingTime(t, repo, "https://github.com/example/gadget.git", tick)
	cache := NewCatalogIdentityCache()
	before := catalogProjectAt(t, cache, repo)
	setRemoteKeepingTime(t, repo, "https://github.com/example/gidget.git", tick)
	later := catalogProjectAt(t, cache, repo)
	if before.ID != "repo:github.com/example/gadget" || later.ID != "repo:github.com/example/gidget" {
		t.Fatalf("before=%s later=%s, want the rewritten remote", before.ID, later.ID)
	}
}

func TestABranchSwitchThatKeepsHEADsSizeAndTimeShowsTheNewBranch(t *testing.T) {
	repo := testkit.GitRepo(t)
	testkit.Git(t, repo, "branch", "dev1")
	head := filepath.Join(repo, ".git", "HEAD")
	tick := inOneTick()
	testkit.RewriteKeepingTime(t, head, "ref: refs/heads/main\n", tick)
	cache := NewCatalogIdentityCache()
	session := surface.Session{Cwd: repo}
	if first := cache.Identity(context.Background(), session, time.Now(), noStoredProject); first.Checkout.Branch != "main" {
		t.Fatalf("first=%+v", first.Checkout)
	}
	testkit.RewriteKeepingTime(t, head, "ref: refs/heads/dev1\n", tick)
	cache.Identity(context.Background(), session, time.Now(), noStoredProject)
	cache.Wait()
	if later := cache.Identity(context.Background(), session, time.Now(), noStoredProject); later.Checkout.Branch != "dev1" {
		t.Fatalf("later=%+v, want the switched branch", later.Checkout)
	}
}

func TestAProjectRootFollowsARemoteRewrittenAtTheSameSizeInOneTick(t *testing.T) {
	repo := testkit.GitRepo(t)
	testkit.Git(t, repo, "remote", "add", "origin", "https://github.com/example/gadget.git")
	tick := inOneTick()
	setRemoteKeepingTime(t, repo, "https://github.com/example/gadget.git", tick)
	cache := NewCatalogIdentityCache()
	beforeKey, _, ok := cache.RootRepository(context.Background(), repo, time.Now())
	if !ok {
		t.Fatal("root not read")
	}
	setRemoteKeepingTime(t, repo, "https://github.com/example/gidget.git", tick)
	laterKey, repository, ok := cache.RootRepository(context.Background(), repo, time.Now())
	if !ok || beforeKey == laterKey || !strings.Contains(string(repository), "gidget") {
		t.Fatalf("before=%q later=%q repository=%s, want the rewritten remote", beforeKey, laterKey, repository)
	}
}

func TestCatalogIdentityResolvesPathWithNewline(t *testing.T) {
	for _, name := range []string{"line\nbreak", "\nleading", "trailing\n"} {
		repo := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(repo, 0700); err != nil {
			t.Fatal(err)
		}
		testkit.Git(t, repo, "init", "-b", "main")
		identity := NewCatalogIdentityCache().Identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
		if identity.Problem != "" || identity.Project == nil || identity.Project.RootPath != identity.Checkout.Path || filepath.Base(identity.Checkout.Path) != name {
			t.Fatalf("%q: identity=%+v", name, identity)
		}
	}
}

func TestCatalogIdentityCacheReusesResolvedCheckout(t *testing.T) {
	repo := t.TempDir()
	testkit.Git(t, repo, "init", "-b", "main")
	testkit.Git(t, repo, "config", "user.email", "test@example.com")
	testkit.Git(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testkit.Git(t, repo, "add", "README.md")
	testkit.Git(t, repo, "commit", "-m", "base")
	testkit.SettleGitFiles(t, repo)
	calls := countGitCalls(t)
	cache := NewCatalogIdentityCache()
	now := time.Now()
	session := surface.Session{Cwd: repo}

	first := cache.Identity(context.Background(), session, now, noStoredProject)
	resolved := calls()
	if resolved == 0 || first.Checkout.Branch != "main" || first.Checkout.Dirty {
		t.Fatalf("calls=%d identity=%+v", resolved, first.Checkout)
	}
	if again := cache.Identity(context.Background(), session, now.Add(time.Minute), noStoredProject); again != first || calls() != resolved {
		t.Fatalf("cached lookup ran %d git commands", calls()-resolved)
	}

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if edited := cache.Identity(context.Background(), session, now.Add(time.Minute), noStoredProject); edited.Checkout.Dirty || calls() != resolved {
		t.Fatalf("working tree edit re-resolved before expiry: dirty=%v calls=%d", edited.Checkout.Dirty, calls()-resolved)
	}
	if expired := cache.Identity(context.Background(), session, now.Add(catalogIdentityTTL), noStoredProject); expired.Checkout.Dirty {
		t.Fatalf("expired entry was not served while it refreshes: %+v", expired.Checkout)
	}
	cache.Wait()
	if refreshed := cache.Identity(context.Background(), session, now.Add(catalogIdentityTTL), noStoredProject); !refreshed.Checkout.Dirty || calls() == resolved {
		t.Fatalf("expired entry was not refreshed: %+v", refreshed.Checkout)
	}

	testkit.Git(t, repo, "checkout", "-q", "-b", "feature/cache")
	switched := calls()
	cache.Identity(context.Background(), session, now.Add(catalogIdentityTTL), noStoredProject)
	cache.Wait()
	if branch := cache.Identity(context.Background(), session, now.Add(catalogIdentityTTL), noStoredProject); branch.Checkout.Branch != "feature/cache" || calls() == switched {
		t.Fatalf("branch switch did not invalidate: %+v", branch.Checkout)
	}
}

func countGitCalls(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\nexec %q \"$@\"\n", logPath, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		raw, _ := os.ReadFile(logPath)
		return strings.Count(string(raw), "\n")
	}
}

func TestARepositoryGainsItsRootCommitWithItsFirstCommit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "fresh")
	testkit.Git(t, t.TempDir(), "init", "-b", "main", repo)
	testkit.Git(t, repo, "config", "user.email", "test@example.com")
	testkit.Git(t, repo, "config", "user.name", "Test")
	repositories := &CatalogRepositoryCache{entries: map[string]catalogRepositoryEntry{}}
	resolve := func() []string {
		identity, _, transient := resolveCatalogIdentity(context.Background(), repo, nil, ProjectLinkSet{}, repositories)
		if transient || identity.Project == nil {
			t.Fatalf("identity=%+v transient=%v", identity, transient)
		}
		return identity.Project.RootCommits
	}
	if roots := resolve(); len(roots) != 0 {
		t.Fatalf("a repository without commits has roots %v", roots)
	}
	testkit.Git(t, repo, "commit", "--allow-empty", "-m", "first")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(head))
	if roots := resolve(); len(roots) != 1 || roots[0] != root {
		t.Fatalf("after the first commit roots=%v want [%s]", roots, root)
	}
}
