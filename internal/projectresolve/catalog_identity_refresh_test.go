package projectresolve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func repoWithOriginHead(t *testing.T, branch string, others ...string) string {
	t.Helper()
	repo := testkit.GitRepo(t)
	if branch != "main" {
		testkit.Git(t, repo, "branch", "-m", "main", branch)
	}
	testkit.Git(t, repo, "remote", "add", "origin", "https://github.com/example/gadget.git")
	for _, name := range append([]string{branch}, others...) {
		testkit.Git(t, repo, "update-ref", "refs/remotes/origin/"+name, "HEAD")
	}
	testkit.Git(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch)
	testkit.SettleGitFiles(t, repo)
	return repo
}

func refreshedIdentity(cache *CatalogIdentityCache, session surface.Session, at time.Time) CatalogIdentity {
	cache.Identity(context.Background(), session, at, noStoredProject)
	cache.Wait()
	return cache.Identity(context.Background(), session, at, noStoredProject)
}

func TestAnExpiredIdentityWhoseGitStateHasNotMovedRereadsOnlyItsStatus(t *testing.T) {
	repo := repoWithOriginHead(t, "main")
	cache := NewCatalogIdentityCache()
	session := surface.Session{Cwd: repo}
	now := time.Now()
	if first := cache.Identity(context.Background(), session, now, noStoredProject); !first.Checkout.IsMain || first.Checkout.Dirty {
		t.Fatalf("first=%+v", first.Checkout)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := countGitCalls(t)
	refreshed := refreshedIdentity(cache, session, now.Add(catalogIdentityTTL))
	if !refreshed.Checkout.Dirty || refreshed.Checkout.Branch != "main" || !refreshed.Checkout.IsMain {
		t.Fatalf("refreshed=%+v, want the edit to show as dirty on the same branch", refreshed.Checkout)
	}
	if got := calls(); got != 1 {
		t.Fatalf("refreshing an identity whose git state had not moved ran %d git commands, want only its status", got)
	}
}

func TestMovingOriginsHeadRefreshesWhetherTheCheckoutIsMain(t *testing.T) {
	repo := repoWithOriginHead(t, "main", "release")
	cache := NewCatalogIdentityCache()
	session := surface.Session{Cwd: repo}
	now := time.Now()
	if first := cache.Identity(context.Background(), session, now, noStoredProject); !first.Checkout.IsMain {
		t.Fatalf("first=%+v, want main to be origin's head", first.Checkout)
	}
	testkit.Git(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/release")
	if later := refreshedIdentity(cache, session, now.Add(catalogIdentityTTL)); later.Checkout.IsMain {
		t.Fatalf("later=%+v, want main no longer the main branch once origin's head moved", later.Checkout)
	}
}

func TestRenamingTheRemoteRefreshesWhetherTheCheckoutIsMain(t *testing.T) {
	repo := repoWithOriginHead(t, "trunk")
	cache := NewCatalogIdentityCache()
	session := surface.Session{Cwd: repo}
	now := time.Now()
	if first := cache.Identity(context.Background(), session, now, noStoredProject); !first.Checkout.IsMain {
		t.Fatalf("first=%+v, want trunk to be origin's head", first.Checkout)
	}
	testkit.Git(t, repo, "remote", "rename", "origin", "upstream")
	if later := cache.Identity(context.Background(), session, now.Add(time.Minute), noStoredProject); later.Checkout.IsMain {
		t.Fatalf("later=%+v, want the first lookup after the rename to show trunk no longer main", later.Checkout)
	}
}

func TestAFolderThatBecomesItsOwnRepositoryMovesItsSessionOnceTheIdentityExpires(t *testing.T) {
	for _, name := range []string{"widget", "widget "} {
		t.Run(name, func(t *testing.T) { folderBecomesItsOwnRepository(t, name) })
	}
}

func folderBecomesItsOwnRepository(t *testing.T, name string) {
	repo := repoWithOriginHead(t, "main")
	if name != filepath.Base(repo) {
		renamed := filepath.Join(filepath.Dir(repo), name)
		if err := os.Rename(repo, renamed); err != nil {
			t.Fatal(err)
		}
		repo = renamed
		testkit.SettleGitFiles(t, repo)
	}
	nested := filepath.Join(repo, "src", "lib")
	cache := NewCatalogIdentityCache()
	session := surface.Session{Cwd: nested}
	now := time.Now()
	first := cache.Identity(context.Background(), session, now, noStoredProject)
	if first.Project == nil || filepath.Base(first.Checkout.Path) != name {
		t.Fatalf("first=%+v, want the enclosing checkout", first.Checkout)
	}
	testkit.Git(t, nested, "init", "-q", "-b", "main")
	later := refreshedIdentity(cache, session, now.Add(catalogIdentityTTL))
	if later.Project == nil || later.Checkout.Path != first.Checkout.Path+"/src/lib" || later.Project.RootPath != later.Checkout.Path {
		t.Fatalf("later checkout=%+v project=%+v, want the nested repository", later.Checkout, later.Project)
	}
}
