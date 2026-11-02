package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestDoctorAndBoardNameAMissingPromptHookOnceTheLedgerCaptures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: "hook-key"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	doctor := func() []promptHookProblem {
		t.Helper()
		output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
		var report struct {
			Problems []promptHookProblem `json:"ledgerPromptHookProblems"`
			Err      string              `json:"ledgerPromptHooksError"`
		}
		if err := json.Unmarshal([]byte(output), &report); err != nil || report.Err != "" {
			t.Fatalf("doctor output=%s err=%v", output, err)
		}
		return report.Problems
	}
	board := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	want := "prompt hook missing in " + root + ": terminal words are not captured live; run agenthail hooks install"

	if problems := doctor(); len(problems) != 0 {
		t.Fatalf("a ledger that captures nothing warned about hooks: %+v", problems)
	}
	if out := board(); strings.Contains(out, "prompt hook") {
		t.Fatalf("board warned before anything captures: %q", out)
	}

	if err := ledger.NewStore(store.WriteDB(), store.ReadDB()).Enable("hook-key"); err != nil {
		t.Fatal(err)
	}
	if problems := doctor(); len(problems) != 1 || problems[0] != (promptHookProblem{Root: root, State: claudeHookMissing}) {
		t.Fatalf("doctor problems %+v", problems)
	}
	text, err := captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if !strings.Contains(text, want) {
		t.Fatalf("doctor text lacks %q:\n%s", want, text)
	}
	if err != nil && strings.Contains(err.Error(), "ledger") {
		t.Fatalf("a root with no session of the enabled project failed doctor: %v", err)
	}

	if err := store.RegisterSession(surface.Session{ID: "terminal", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: dir, Status: surface.StatusIdle, Transcript: filepath.Join(root, "projects", "p", "terminal.jsonl"), LastActive: time.Now().Add(-24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	for i := range 501 {
		id := fmt.Sprintf("newer-%03d", i)
		if err := store.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: elsewhere, Status: surface.StatusIdle, Transcript: filepath.Join(root, "projects", "q", id+".jsonl"), LastActive: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if problems := doctor(); len(problems) != 1 || problems[0].EnabledProject != "hook-key" {
		t.Fatalf("a root holding the enabled project's sessions is not a doctor problem: %+v", problems)
	}
	text, err = captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if err == nil || !strings.Contains(err.Error(), "enabled ledger captures no terminal words") || !strings.Contains(text, "the ledger is enabled for hook-key") {
		t.Fatalf("doctor did not fail on the enabled project's root: err=%v\n%s", err, text)
	}
	if out := board(); !strings.Contains(out, want) {
		t.Fatalf("board lacks %q:\n%s", want, out)
	}
	var boardJSON struct {
		Problems []promptHookProblem `json:"promptHookProblems"`
	}
	if err := json.Unmarshal([]byte(board("--json")), &boardJSON); err != nil || len(boardJSON.Problems) != 1 {
		t.Fatalf("board json problems %+v err %v", boardJSON.Problems, err)
	}

	if _, err := installClaudeHooks(filepath.Join(root, "settings.json"), "/opt/agenthail/bin/agenthail"); err != nil {
		t.Fatal(err)
	}
	if problems := doctor(); len(problems) != 0 {
		t.Fatalf("an installed prompt hook still warned: %+v", problems)
	}
	if out := board(); strings.Contains(out, "prompt hook") {
		t.Fatalf("board warned with the hook installed: %q", out)
	}
}

func TestACoordinatorAloneMakesThePromptHookMatter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj-c", WorkspaceRoot: dir, Title: "proj-c", Key: "coord-key"}); err != nil {
		t.Fatal(err)
	}
	if problems, err := ledgerPromptHookProblems(context.Background(), store); err != nil || len(problems) != 0 {
		t.Fatalf("problems %+v err %v", problems, err)
	}
	transcript := filepath.Join(home, ".claude", "projects", "p", "coord-session.jsonl")
	if err := store.RegisterSession(surface.Session{ID: "coord-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: dir, Status: surface.StatusIdle, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCoordinator(registry.CoordinatorSpec{ProjectID: "proj-c", ProjectRoot: dir, Name: "coord", Surface: "claude", Cwd: dir}, registry.CoordinatorChange{SessionID: "coord-session"}); err != nil {
		t.Fatal(err)
	}
	problems, err := ledgerPromptHookProblems(context.Background(), store)
	if err != nil || len(problems) != 1 || problems[0].State != claudeHookMissing {
		t.Fatalf("a coordinator did not make the hook matter: %+v err %v", problems, err)
	}
	if problems[0].EnabledProject != "" {
		t.Fatalf("coordinator-only capture must stay a warning: %+v", problems[0])
	}
}
