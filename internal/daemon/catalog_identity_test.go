package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

func TestCatalogIdentityGroupsWorktreesByCommonDirectory(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-m", "base")
	child := filepath.Join(t.TempDir(), "child")
	gitRun(t, repo, "worktree", "add", "-b", "feature/catalog", child)
	if err := os.WriteFile(filepath.Join(child, "README.md"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}

	base := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: repo}, time.Now())
	worktree := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: child}, time.Now())
	canonicalRepo, err := workspace.NormalizeCWD(repo)
	if err != nil {
		t.Fatal(err)
	}
	canonicalChild, err := workspace.NormalizeCWD(child)
	if err != nil {
		t.Fatal(err)
	}
	if base.UnavailableReason != "" || worktree.UnavailableReason != "" {
		t.Fatalf("base=%+v worktree=%+v", base, worktree)
	}
	if base.HostProject.ID != worktree.HostProject.ID || base.HostProject.CommonDir == "" || worktree.HostProject.CommonDir == "" {
		t.Fatalf("base=%+v worktree=%+v", base.HostProject, worktree.HostProject)
	}
	if base.Checkout.ID == worktree.Checkout.ID || base.Checkout.Path != canonicalRepo || worktree.Checkout.Path != canonicalChild {
		t.Fatalf("base=%+v worktree=%+v", base.Checkout, worktree.Checkout)
	}
	if !base.Checkout.IsMain || worktree.Checkout.Branch != "feature/catalog" || !worktree.Checkout.Dirty {
		t.Fatalf("base=%+v worktree=%+v", base.Checkout, worktree.Checkout)
	}
}

func TestCatalogIdentityRetainsNonGitWorkspace(t *testing.T) {
	path := t.TempDir()
	identity := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: path}, time.Now())
	canonicalPath, err := workspace.NormalizeCWD(path)
	if err != nil {
		t.Fatal(err)
	}
	if identity.UnavailableReason != "" || identity.HostProject.Path != canonicalPath || identity.HostProject.CommonDir != "" || identity.Checkout.Path != canonicalPath {
		t.Fatalf("identity=%+v", identity)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
