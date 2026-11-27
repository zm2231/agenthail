package surface

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func removedWorktree(t *testing.T) (home, checkout, worktree string) {
	t.Helper()
	home = t.TempDir()
	checkout = filepath.Join(home, "work", "repo")
	worktree = filepath.Join(checkout, ".worktrees", "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	return home, checkout, worktree
}

func TestAWorktreeWorkerWhoseWorktreeIsRemovedResumesInItsCheckout(t *testing.T) {
	home, checkout, worktree := removedWorktree(t)
	move, err := FallbackFolder(filepath.Join(worktree, "sub"), home, []string{worktree})
	if err != nil {
		t.Fatal(err)
	}
	if move.Into != checkout || !move.Checkout {
		t.Fatalf("resumed in %+v, want the checkout %s", move, checkout)
	}
	if line := move.Line(); !strings.HasPrefix(line, "[agenthail] ") || !strings.Contains(line, "a shared checkout. Don't edit or commit here") || !strings.Contains(line, filepath.Join(checkout, ".worktrees")) {
		t.Fatalf("line %q", line)
	}
}

func TestASessionWhoseRootsAreElsewhereIsRefusedRatherThanMovedUp(t *testing.T) {
	home, _, worktree := removedWorktree(t)
	elsewhere := filepath.Join(home, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := FallbackFolder(worktree, home, []string{elsewhere})
	if err == nil || !strings.Contains(err.Error(), "outside the folders it may work in") || !strings.Contains(err.Error(), "recreate "+worktree+", or archive the session") {
		t.Fatalf("err = %v", err)
	}
}

func TestAFolderWhoseOnlyRemainingParentIsHomeIsRefused(t *testing.T) {
	home := t.TempDir()
	gone := filepath.Join(home, "project", "deeper")
	if _, err := FallbackFolder(gone, home, nil); err == nil || !strings.Contains(err.Error(), "the nearest folder that remains is "+home) {
		t.Fatalf("err = %v", err)
	}
	if _, err := FallbackFolder(gone, filepath.Join(home, "nested", "home"), nil); err == nil {
		t.Fatal("a folder above home was offered")
	}
}

func TestAWorktreeWhoseWorktreesFolderIsAlsoGoneStillResumesInItsCheckoutAsShared(t *testing.T) {
	home, checkout, worktree := removedWorktree(t)
	if err := os.RemoveAll(filepath.Dir(worktree)); err != nil {
		t.Fatal(err)
	}
	move, err := FallbackFolder(worktree, home, nil)
	if err != nil || move.Into != checkout || !move.Checkout {
		t.Fatalf("move=%+v err=%v, want the checkout %s as shared", move, err, checkout)
	}
}

func TestAFolderGoneInsideARemainingWorktreeResumesInThatWorktree(t *testing.T) {
	home, _, worktree := removedWorktree(t)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	move, err := FallbackFolder(filepath.Join(worktree, "sub"), home, []string{worktree})
	if err != nil || move.Into != worktree || move.Checkout {
		t.Fatalf("move=%+v err=%v, want the worktree %s", move, err, worktree)
	}
}
