package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehooks"
	"github.com/zm2231/agenthail/internal/daemon"
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

	if err := ledger.NewStore(store.WriteDB(), store.ReadDB()).Enable("hook-key", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if problems := doctor(); len(problems) != 1 || problems[0] != (promptHookProblem{Root: root, State: claudehooks.Missing}) {
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

	if _, err := claudehooks.InstallAt(filepath.Join(root, "settings.json"), "/opt/agenthail/bin/agenthail"); err != nil {
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
	if err != nil || len(problems) != 1 || problems[0].State != claudehooks.Missing {
		t.Fatalf("a coordinator did not make the hook matter: %+v err %v", problems, err)
	}
	if problems[0].EnabledProject != "" {
		t.Fatalf("coordinator-only capture must stay a warning: %+v", problems[0])
	}
}

func TestAnUnresolvedFolderKeepsTheProblemAndFailsDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj-u", WorkspaceRoot: dir, Title: "proj-u", Key: "unresolved-key"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := ledger.NewStore(store.WriteDB(), store.ReadDB()).Enable("unresolved-key", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	unknown := t.TempDir()
	if err := store.RegisterSession(surface.Session{ID: "unknown-folder", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: unknown, Status: surface.StatusIdle, Transcript: filepath.Join(root, "projects", "u", "unknown-folder.jsonl")}); err != nil {
		t.Fatal(err)
	}
	resolve := resolveSessionProject
	resolveSessionProject = func(ctx context.Context, r *registry.Registry, path string) (*daemon.CatalogProject, error) {
		if path == unknown {
			return nil, errors.New("repository identity is unavailable")
		}
		return resolve(ctx, r, path)
	}
	t.Cleanup(func() { resolveSessionProject = resolve })

	var board struct {
		Problems []promptHookProblem `json:"promptHookProblems"`
		Err      string              `json:"promptHooksError"`
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"--json"}) })
	if err != nil || json.Unmarshal([]byte(out), &board) != nil {
		t.Fatalf("board json %q err %v", out, err)
	}
	if len(board.Problems) != 1 || board.Problems[0].Root != root || !strings.Contains(board.Err, "repository identity is unavailable") {
		t.Fatalf("board json must keep the problem beside the error: %+v", board)
	}
	text, err := captureStdout(t, func() error { return app.cmdLedger(nil) })
	if err != nil || !strings.Contains(text, "prompt hook missing in "+root) {
		t.Fatalf("board text err=%v\n%s", err, text)
	}
	var report struct {
		OK bool `json:"ok"`
	}
	output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	if json.Unmarshal([]byte(output), &report) != nil || report.OK {
		t.Fatalf("doctor was ok while a problem root's ownership is unknown: %s", output)
	}
}

func TestAnExpiredDeadlineStopsTheScanBeforeAnyUnrelatedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	if err := ledger.NewStore(store.WriteDB(), store.ReadDB()).Enable("expired-key", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	places := localSessionPlaces
	localSessionPlaces = func(*registry.Registry, context.Context, surface.SurfaceKind) ([]registry.SessionPlace, error) {
		return []registry.SessionPlace{{Cwd: "/elsewhere", Transcript: "/not/a/config/root/projects/x/s.jsonl"}}, nil
	}
	t.Cleanup(func() { localSessionPlaces = places })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	problems, err := ledgerPromptHookProblems(ctx, store)
	if err == nil || !errors.Is(err, context.Canceled) || len(problems) != 1 {
		t.Fatalf("an expired scan must report its deadline beside the problem: %+v %v", problems, err)
	}
}

func TestADeadlineOnTheSessionScanKeepsTheProblemAndFailsDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj-d", WorkspaceRoot: dir, Title: "proj-d", Key: "deadline-key"}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.NewStore(store.WriteDB(), store.ReadDB()).Enable("deadline-key", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	places := localSessionPlaces
	localSessionPlaces = func(*registry.Registry, context.Context, surface.SurfaceKind) ([]registry.SessionPlace, error) {
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { localSessionPlaces = places })

	var report struct {
		OK       bool                `json:"ok"`
		Problems []promptHookProblem `json:"ledgerPromptHookProblems"`
		Err      string              `json:"ledgerPromptHooksError"`
	}
	output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("doctor output=%s err=%v", output, err)
	}
	if report.OK || len(report.Problems) != 1 || report.Problems[0].Root != root || !strings.Contains(report.Err, "deadline") {
		t.Fatalf("a scan deadline must keep the problem and fail doctor: %+v", report)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"doctor"}) }); err == nil || !strings.Contains(err.Error(), "enabled ledger") {
		t.Fatalf("text doctor did not fail: %v", err)
	}
}
