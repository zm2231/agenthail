package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

func noStoredProject() *CatalogProject { return nil }

func catalogTestRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(filepath.Join(repo, "src", "lib"), 0700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-m", "base")
	return repo
}

func TestCatalogIdentityMapsWorktreesAndSubfoldersToTheMainCheckout(t *testing.T) {
	repo := catalogTestRepo(t)
	child := filepath.Join(repo, ".worktrees", "task-a1")
	gitRun(t, repo, "worktree", "add", "-b", "feature/catalog", child)
	if err := os.WriteFile(filepath.Join(child, "README.md"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cache := newCatalogIdentityCache()
	base := cache.identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
	worktree := cache.identity(context.Background(), surface.Session{Cwd: child}, time.Now(), noStoredProject)
	nested := cache.identity(context.Background(), surface.Session{Cwd: filepath.Join(repo, "src", "lib")}, time.Now(), noStoredProject)
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	canonicalChild, err := workspace.NormalizeCWD(child)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []catalogIdentity{base, worktree, nested} {
		if identity.UnavailableReason != "" || identity.Project == nil {
			t.Fatalf("identity=%+v", identity)
		}
		if identity.Project.ID != base.Project.ID || identity.Project.DisplayName != "widget" || identity.Project.RootPath != canonicalRepo || identity.Project.Kind != catalogProjectGit {
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
	repo := catalogTestRepo(t)
	child := filepath.Join(repo, ".worktrees", "task-b2")
	gitRun(t, repo, "worktree", "add", "-b", "feature/removed", child)
	gitRun(t, repo, "worktree", "remove", child)
	identity := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: child}, time.Now(), noStoredProject)
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	if identity.UnavailableReason != "" || identity.Project == nil || identity.Project.RootPath != canonicalRepo || identity.Checkout.Path != filepath.Join(canonicalRepo, ".worktrees", "task-b2") {
		t.Fatalf("identity=%+v project=%+v", identity, identity.Project)
	}
	gone := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: filepath.Join(t.TempDir(), "scratch", "gone")}, time.Now(), noStoredProject)
	if gone.Project != nil || gone.UnavailableReason != "" {
		t.Fatalf("gone=%+v", gone)
	}
}

func TestCatalogIdentityLeavesUnregisteredFoldersOutOfProjects(t *testing.T) {
	path := t.TempDir()
	identity := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: path}, time.Now(), noStoredProject)
	canonicalPath, err := workspace.NormalizeCWD(path)
	if err != nil {
		t.Fatal(err)
	}
	if identity.UnavailableReason != "" || identity.Project != nil || identity.Checkout.Path != canonicalPath {
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
	cache := newCatalogIdentityCache()
	cache.setProjects([]registry.ProjectRecord{
		{ID: "p-outer", WorkspaceRoot: outer, Title: "notes", Added: true},
		{ID: "p-inner", WorkspaceRoot: inner, Title: "drafts", Added: true},
		{ID: "implicit:" + root, WorkspaceRoot: root, Title: "root"},
	})
	identity := cache.identity(context.Background(), surface.Session{Cwd: work}, time.Now(), noStoredProject)
	if identity.Project == nil || identity.Project.ID != "p-inner" || identity.Project.LocalID != "p-inner" || !identity.Project.Added || identity.Project.Kind != catalogProjectFolder || identity.Project.RootPath != inner {
		t.Fatalf("identity=%+v", identity.Project)
	}
	if outside := cache.identity(context.Background(), surface.Session{Cwd: root}, time.Now(), noStoredProject); outside.Project != nil {
		t.Fatalf("outside=%+v", outside.Project)
	}
}

func TestCatalogIdentityKeepsTheKnownProjectWhenGitFails(t *testing.T) {
	repo := catalogTestRepo(t)
	cache := newCatalogIdentityCache()
	resolved := cache.identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
	if resolved.Project == nil {
		t.Fatalf("resolved=%+v", resolved)
	}
	t.Setenv("PATH", t.TempDir())
	later := time.Now().Add(catalogIdentityTTL)
	cache.identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	cache.wait()
	failed := cache.identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	if failed.UnavailableReason == "" || failed.Project == nil || failed.Project.ID != resolved.Project.ID {
		t.Fatalf("failed=%+v resolved=%+v", failed, resolved.Project)
	}
	stored := &CatalogProject{ID: "project-stored", DisplayName: "widget", RootPath: repo, Kind: catalogProjectGit}
	fresh := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: repo}, later, func() *CatalogProject { return stored })
	if fresh.UnavailableReason == "" || fresh.Project != stored {
		t.Fatalf("fresh=%+v", fresh)
	}
}

func TestCatalogIdentityKeepsTheProjectWhenGitRefusesTheRepository(t *testing.T) {
	repo := catalogTestRepo(t)
	cache := newCatalogIdentityCache()
	resolved := cache.identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
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
	cache.identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	cache.wait()
	refused := cache.identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	if refused.UnavailableReason == "" || refused.Project == nil || refused.Project.ID != resolved.Project.ID {
		t.Fatalf("refused=%+v resolved=%+v", refused, resolved.Project)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func catalogProjectAt(t *testing.T, cache *catalogIdentityCache, path string) *CatalogProject {
	t.Helper()
	identity := cache.identity(context.Background(), surface.Session{Cwd: path}, time.Now(), noStoredProject)
	if identity.UnavailableReason != "" || identity.Project == nil {
		t.Fatalf("identity for %s: %+v", path, identity)
	}
	return identity.Project
}

func TestImplicitProjectsShareARepositoryOnlyThroughItsRemote(t *testing.T) {
	first := catalogTestRepo(t)
	gitRun(t, first, "remote", "add", "origin", "https://github.com/Example/Gadget.git")
	gitRun(t, first, "remote", "add", "upstream", "https://github.com/upstream-org/gadget.git")
	second := filepath.Join(t.TempDir(), "checkout-two")
	gitRun(t, first, "clone", "--quiet", first, second)
	gitRun(t, second, "remote", "set-url", "origin", "git@github.com:example/gadget")
	alone := filepath.Join(t.TempDir(), "alone")
	gitRun(t, first, "clone", "--quiet", first, alone)
	gitRun(t, alone, "remote", "remove", "origin")

	cache := newCatalogIdentityCache()
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
	gitRun(t, bare, "init", "--bare")
	gitRun(t, bare, "remote", "add", "team.backup", "/srv/git/mirror.git")
	gitRun(t, bare, "remote", "add", "team.main", "https://github.com/example/mirror.git")
	gitRun(t, bare, "config", "--add", "remote.team.main.url", "https://github.com/example/mirror-second.git")
	canonicalBare, err := workspace.NormalizeCWD(bare)
	if err != nil {
		t.Fatal(err)
	}
	cache := newCatalogIdentityCache()
	found := catalogProjectAt(t, cache, filepath.Join(bare, "refs"))
	if found.ID != "repo:github.com/example/mirror" || found.RootPath != canonicalBare || found.DisplayName != "mirror" || found.Repository == nil || found.Repository.RemoteName != "team.main" {
		t.Fatalf("bare repository: %+v repository=%+v", found, found.Repository)
	}
}

func TestChangingOriginMovesTheProject(t *testing.T) {
	repo := catalogTestRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "https://github.com/example/gadget.git")
	cache := newCatalogIdentityCache()
	before := catalogProjectAt(t, cache, repo)
	gitRun(t, repo, "remote", "set-url", "origin", "https://github.com/someone-else/gadget.git")
	later := cache.identity(context.Background(), surface.Session{Cwd: repo}, time.Now(), noStoredProject)
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
	cache := newCatalogIdentityCache()
	now := time.Now()
	var repos []string
	for range catalogIdentityRefreshes + 2 {
		repo := catalogTestRepo(t)
		repos = append(repos, repo)
		cache.identity(context.Background(), surface.Session{Cwd: repo}, now, noStoredProject)
	}
	release := gateGit(t)
	defer cache.wait()
	defer release()
	later := now.Add(catalogIdentityTTL)
	started := time.Now()
	for range 2 {
		for _, repo := range repos {
			if identity := cache.identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject); identity.Project == nil {
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
	cache.wait()
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
	repo := catalogTestRepo(t)
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
	cache := newCatalogIdentityCache()
	now := time.Now()
	failed := cache.identity(context.Background(), surface.Session{Cwd: repo}, now, noStoredProject)
	if failed.UnavailableReason == "" {
		t.Fatalf("failed=%+v", failed)
	}
	before := calls()
	if again := cache.identity(context.Background(), surface.Session{Cwd: repo}, now.Add(time.Minute), noStoredProject); again.UnavailableReason == "" || calls() != before {
		t.Fatalf("failed workspace read again: %d git calls", calls()-before)
	}
}
