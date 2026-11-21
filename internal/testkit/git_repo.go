package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func GitRepo(t testing.TB) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(filepath.Join(repo, "src", "lib"), 0700); err != nil {
		t.Fatal(err)
	}
	Git(t, repo, "init", "-b", "main")
	Git(t, repo, "config", "user.email", "test@example.com")
	Git(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	Git(t, repo, "add", "README.md")
	Git(t, repo, "commit", "-m", "base")
	return repo
}

// SettleGitFiles moves every file under repo's .git an hour back, as for a
// repository nobody has touched since well before the daemon read it.
func SettleGitFiles(t testing.TB, repo string) {
	t.Helper()
	settled := time.Now().Add(-time.Hour)
	err := filepath.WalkDir(filepath.Join(repo, ".git"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, settled, settled)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func Git(t testing.TB, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
