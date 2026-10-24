package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestADaemonGitStatusTakesNoIndexLock(t *testing.T) {
	repo := t.TempDir()
	file := filepath.Join(repo, "notes.txt")
	if err := os.WriteFile(file, []byte("notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "add", "notes.txt")
	gitRun(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "notes")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(repo, ".git", "index")
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := catalogGit(context.Background(), repo, "status", "--porcelain=v1", "-z", "--untracked-files=normal"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	// Status refreshes a stale index through index.lock; a kill on the deadline in that window strands the lock.
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the daemon's git status rewrote the index, so it took index.lock")
	}
}
