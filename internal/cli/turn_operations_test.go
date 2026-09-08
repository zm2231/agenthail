package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestThreadCreateClaudeAndAdvancedOptionsAreParsed(t *testing.T) {
	cwd := t.TempDir()
	schema := filepath.Join(cwd, "schema.json")
	os.WriteFile(schema, []byte(`{"type":"object"}`), 0600)
	request, err := parseThreadCreateRequest([]string{"create", "claude", "hello", "--cwd", cwd, "--effort", "high", "--worktree", "branch", "--permission-mode", "plan"})
	if err != nil || request.options.Effort != "high" || request.options.Worktree != "branch" || request.options.Message != "hello" {
		t.Fatal(request, err)
	}
	options, err := parseTurnOptions([]string{"--output-schema", schema, "--mode", "plan"})
	if err != nil || len(options.OutputSchema) == 0 {
		t.Fatal(options, err)
	}
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}, session: &surface.Session{ID: "claude-new", Surface: surface.KindClaude, Cwd: cwd}}
	app := threadFixture(t, starter)
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "claude", "hello", "--cwd", cwd, "--effort", "high", "--json"})
	})
	if err != nil || !strings.Contains(output, `"id":"claude-new"`) || starter.options[0].Effort != "high" {
		t.Fatal(output, err, starter.options)
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

func TestThreadOperationRejectsIgnoredOptions(t *testing.T) {
	for _, args := range [][]string{{"fork", "target", "--effort", "high"}, {"stop", "target", "--model", "other"}, {"queue", "target", "add", "--message", "ignored"}} {
		if err := validateThreadOperationArgs(args); err == nil {
			t.Fatal(args)
		}
	}
}
