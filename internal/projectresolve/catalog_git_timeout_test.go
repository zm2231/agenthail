package projectresolve

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// slowGit puts a git first on PATH that runs the real one, except that the
// call naming slow runs stall instead.
func slowGit(t *testing.T, slow, stall string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = " + slow + " ] && " + stall + "; done\nexec " + shellQuoted(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// macOS checks a new executable on its first run, which can take longer than the budget.
	if output, err := exec.Command(filepath.Join(bin, "git"), "--version").CombinedOutput(); err != nil {
		t.Fatalf("the stand-in git: %v %s", err, output)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuoted(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, output)
	}
}

func TestARepositoryLookupThatRunsOutOfTimeSaysSoAndNamesTheGitStep(t *testing.T) {
	checkout := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "widget")
		gitIn(t, filepath.Dir(dir), "init", "-q", dir)
		gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
		return dir
	}
	bare := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "widget.git")
		gitIn(t, filepath.Dir(dir), "init", "-q", "--bare", dir)
		return dir
	}
	for _, c := range []struct {
		name, slow, stall, step string
		repository              func(*testing.T) string
	}{
		{"the first read", "--show-toplevel", "exec sleep 5", "rev-parse --show-toplevel", checkout},
		{"a git whose child keeps its output open", "--show-toplevel", "sleep 5 & wait", "rev-parse --show-toplevel", checkout},
		{"the root commits", "rev-list", "exec sleep 5", "rev-list --max-parents=0 HEAD", checkout},
		{"the bare check", "--is-bare-repository", "exec sleep 5", "rev-parse --is-bare-repository", bare},
	} {
		t.Run(c.name, func(t *testing.T) {
			folder := c.repository(t)
			slowGit(t, c.slow, c.stall)
			started := time.Now()
			_, notRepository, err := ResolveCatalogRepository(context.Background(), folder, nil, time.Second)
			var timeout *CatalogGitTimeoutError
			if !errors.As(err, &timeout) || notRepository {
				t.Fatalf("a git that outlasts the budget answered %v (not a repository: %v), want a timeout", err, notRepository)
			}
			if message := err.Error(); !strings.HasPrefix(message, "git took longer than 1s reading ") || !strings.HasSuffix(message, "(git "+c.step+")") || strings.Contains(message, "killed") {
				t.Fatalf("the timeout reads %q, want it to name git %s", message, c.step)
			}
			if elapsed := time.Since(started); elapsed > 4*time.Second {
				t.Fatalf("the lookup took %s, want it to end soon after its budget", elapsed)
			}
		})
	}
}

func TestAGitAnswerThatReachesTheLookupAfterItsBudgetIsGitsAnswer(t *testing.T) {
	answerLate := func(t *testing.T, answer string) string {
		pidFile := filepath.Join(t.TempDir(), "holder.pid")
		t.Cleanup(func() {
			if pid, err := os.ReadFile(pidFile); err == nil {
				exec.Command("kill", strings.TrimSpace(string(pid))).Run()
			}
		})
		return "{ sleep 3 & echo $! > " + shellQuoted(pidFile) + "; " + answer + "; }"
	}
	t.Run("a refusal", func(t *testing.T) {
		folder := filepath.Join(t.TempDir(), "widget")
		gitIn(t, filepath.Dir(folder), "init", "-q", folder)
		slowGit(t, "--show-toplevel", answerLate(t, "echo 'fatal: detected dubious ownership in repository' >&2; exit 128"))
		_, notRepository, err := ResolveCatalogRepository(context.Background(), folder, nil, time.Second)
		var timeout *CatalogGitTimeoutError
		if err == nil || errors.As(err, &timeout) || notRepository || !strings.Contains(err.Error(), "dubious ownership") {
			t.Fatalf("a refusal git gave after the budget answered %v (not a repository: %v), want the refusal", err, notRepository)
		}
	})
	t.Run("no root commits", func(t *testing.T) {
		folder := filepath.Join(t.TempDir(), "widget")
		gitIn(t, filepath.Dir(folder), "init", "-q", folder)
		gitIn(t, folder, "commit", "-q", "--allow-empty", "-m", "first")
		slowGit(t, "rev-list", answerLate(t, "echo 'fatal: bad revision' >&2; exit 128"))
		repository, _, err := ResolveCatalogRepository(context.Background(), folder, nil, time.Second)
		if err != nil || repository.Toplevel == "" {
			t.Fatalf("a rev-list git answered after the budget gave %+v, %v; want the repository without root commits", repository, err)
		}
	})
}
