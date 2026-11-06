// Package worktreetest makes real git repositories for worktree tests.
package worktreetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// PushedCheckout is a checkout of a bare remote whose main holds one pushed
// commit with an app folder, tracking origin/main. Git reads no user or
// system configuration.
func PushedCheckout(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	for key, value := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com"} {
		t.Setenv(key, value)
	}
	remote := filepath.Join(root, "remote.git")
	Git(t, root, "init", "--quiet", "--bare", "--initial-branch=main", remote)
	checkout := filepath.Join(root, "repo")
	Git(t, root, "clone", "--quiet", remote, checkout)
	if err := os.MkdirAll(filepath.Join(checkout, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "app", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, checkout, "add", ".")
	Git(t, checkout, "commit", "--quiet", "-m", "first")
	Git(t, checkout, "push", "--quiet", "origin", "HEAD:main")
	Git(t, checkout, "branch", "--quiet", "--set-upstream-to=origin/main")
	resolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
