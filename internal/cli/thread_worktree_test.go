package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/worktree"
	"github.com/zm2231/agenthail/internal/worktree/worktreetest"
)

func TestThreadCreateInAWorktreeStartsTheThreadThereAndRecordsIt(t *testing.T) {
	checkout := worktreetest.PushedCheckout(t)
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindCodex}, session: &surface.Session{ID: "thread-wt", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}}
	app := threadFixture(t, starter)
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "fix it", "--cwd", filepath.Join(checkout, "app"), "--worktree", "--branch", "fix/it", "--json"})
	})
	if err != nil {
		t.Fatal(output, err)
	}
	path := filepath.Join(checkout, worktree.Dir, "fix-it")
	var answer threadCreateOutput
	if err := json.Unmarshal([]byte(output), &answer); err != nil || answer.Worktree == nil || answer.Worktree.Path != path || answer.Worktree.Branch != "fix/it" || answer.Worktree.Base != "main" {
		t.Fatalf("output %s err=%v", output, err)
	}
	if got := starter.options[0].Cwd; got != filepath.Join(path, "app") {
		t.Fatalf("started in %s", got)
	}
	work, found, err := app.registry().WorkForSession("thread-wt")
	if err != nil || !found || work.State != registry.WorkActive || work.WorktreePath != path {
		t.Fatalf("work %+v found=%t err=%v", work, found, err)
	}
}

func TestThreadCreateTakesBackTheWorktreeWhenNothingStarted(t *testing.T) {
	checkout := worktreetest.PushedCheckout(t)
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindCodex}, err: errors.New("no app server")}
	app := threadFixture(t, starter)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "fix it", "--cwd", checkout, "--worktree", "--branch", "fix/it"})
	}); err == nil {
		t.Fatal("a failed start reported success")
	}
	if _, err := os.Stat(filepath.Join(checkout, worktree.Dir, "fix-it")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree left behind: %v", err)
	}
	if branches := worktreetest.Git(t, checkout, "branch", "--list", "fix/it"); branches != "" {
		t.Fatalf("branch left behind: %q", branches)
	}
	if _, found, err := app.registry().WorkAt(filepath.Join(checkout, worktree.Dir, "fix-it")); err != nil || found {
		t.Fatalf("record left behind: found=%t err=%v", found, err)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "fix it", "--cwd", checkout, "--branch", "fix/it"})
	}); err == nil {
		t.Fatal("--branch without --worktree was accepted")
	}
}
