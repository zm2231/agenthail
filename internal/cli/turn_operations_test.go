package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestThreadCreateClaudeForwardsAdvancedOptions(t *testing.T) {
	cwd := t.TempDir()
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}, session: &surface.Session{ID: "claude-new", Surface: surface.KindClaude, Cwd: cwd}}
	app := threadFixture(t, starter)
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "claude", "hello", "--cwd", cwd, "--effort", "high", "--worktree", "branch", "--permission-mode", "plan", "--json"})
	})
	if err != nil || !strings.Contains(output, `"id":"claude-new"`) || len(starter.options) != 1 {
		t.Fatal(output, err, starter.options)
	}
	if got := starter.options[0]; got.Effort != "high" || got.Worktree != "branch" || got.Message != "hello" {
		t.Fatalf("options=%+v", got)
	}
}

func TestThreadOperationJSONFailurePreservesUnknown(t *testing.T) {
	adapter := &lifecycleCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}}
	app, _ := cliFixture(t, adapter.cliSurface)
	app.Surfaces[0].Surface = adapter
	session := surface.Session{ID: "stopped", Surface: surface.KindClaude}
	if err := app.Registry.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := app.Registry.SetAlias("worker", session.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"@worker", "claude:stopped"} {
		output, err := captureStdout(t, func() error { return app.Run([]string{"thread", "resume", target, "--json"}) })
		if err == nil || !strings.Contains(output, `"unknown":true`) || !strings.Contains(output, `"ok":false`) {
			t.Fatal(output, err)
		}
	}
}

type lifecycleCLISurface struct{ *cliSurface }

func (*lifecycleCLISurface) SessionAction(context.Context, *surface.Session, string) (map[string]any, error) {
	return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("native reply lost"))
}
