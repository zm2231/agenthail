package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

func noStoredProject() *catalogProject { return nil }

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

func TestCatalogIdentityUsesTheNearestRegisteredFolder(t *testing.T) {
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
	cache.setFolders([]registry.ProjectRecord{
		{ID: "project-outer", Kind: catalogProjectFolder, RootPath: outer, DisplayName: "notes", Registered: true},
		{ID: "project-inner", Kind: catalogProjectFolder, RootPath: inner, DisplayName: "drafts", Registered: true},
		{ID: "project-archived-only", Kind: catalogProjectGit, RootPath: root, DisplayName: "root"},
	})
	identity := cache.identity(context.Background(), surface.Session{Cwd: work}, time.Now(), noStoredProject)
	if identity.Project == nil || identity.Project.ID != "project-inner" || identity.Project.Kind != catalogProjectFolder || identity.Project.RootPath != inner {
		t.Fatalf("identity=%+v", identity)
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
	failed := cache.identity(context.Background(), surface.Session{Cwd: repo}, later, noStoredProject)
	if failed.UnavailableReason == "" || failed.Project == nil || failed.Project.ID != resolved.Project.ID {
		t.Fatalf("failed=%+v resolved=%+v", failed, resolved.Project)
	}
	stored := &catalogProject{ID: "project-stored", DisplayName: "widget", RootPath: repo, Kind: catalogProjectGit}
	fresh := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: repo}, later, func() *catalogProject { return stored })
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
	refused := cache.identity(context.Background(), surface.Session{Cwd: repo}, time.Now().Add(catalogIdentityTTL), noStoredProject)
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
