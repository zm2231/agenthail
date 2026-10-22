package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestThreadCreateClaudeForwardsAdvancedOptions(t *testing.T) {
	cwd := t.TempDir()
	starter := &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}, session: &surface.Session{ID: "claude-new", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: cwd}}
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
	session := surface.Session{ID: "stopped", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
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

func TestThreadResumeRegistersTheSessionItReached(t *testing.T) {
	adapter := &resumingCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}}
	app, _ := cliFixture(t, adapter.cliSurface)
	app.Surfaces[0].Surface = adapter
	session := surface.Session{ID: "job", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeBackground, Status: surface.StatusOffline}
	if err := app.Registry.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := app.Registry.SetAlias("worker", session.ID); err != nil {
		t.Fatal(err)
	}
	if output, err := captureStdout(t, func() error { return app.Run([]string{"thread", "resume", "@worker", "--json"}) }); err != nil {
		t.Fatal(output, err)
	}
	stored, err := app.Registry.Session("job")
	if err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.PID != 4242 {
		t.Fatalf("stored=%+v err=%v, want the host process it reached", stored, err)
	}
}

type resumingCLISurface struct{ *cliSurface }

func (*resumingCLISurface) SessionAction(_ context.Context, session *surface.Session, action string) (map[string]any, error) {
	if action == "resume" {
		session.RuntimeKind, session.PID, session.Transport, session.Status = surface.RuntimeClaudeHost, 4242, "uds", surface.StatusIdle
	}
	return map[string]any{"sessionId": session.ID, "action": action}, nil
}

type lifecycleCLISurface struct{ *cliSurface }

func (*lifecycleCLISurface) SessionAction(context.Context, *surface.Session, string) (map[string]any, error) {
	return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("native reply lost"))
}

func TestThreadForkPassesLastTurnAndRejectsRemovedFlags(t *testing.T) {
	source := surface.Session{ID: "source", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: "/source"}
	adapter := &forkCLISurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"source": source}}}
	app, _ := cliFixture(t, adapter.cliSurface)
	app.Surfaces[0].Surface = adapter
	if err := app.Registry.RegisterSession(source); err != nil {
		t.Fatal(err)
	}
	if err := app.Registry.SetAlias("src", "source"); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "fork", "@src", "--last-turn", "turn-2", "--json"})
	})
	if err != nil || !strings.Contains(output, `"id":"forked"`) || len(adapter.options) != 1 || adapter.options[0] != (surface.ForkOptions{LastTurnID: "turn-2"}) {
		t.Fatal(output, err, adapter.options)
	}
	for _, flag := range []string{"--cwd", "--before-turn"} {
		if _, err := captureStdout(t, func() error { return app.Run([]string{"thread", "fork", "@src", flag, "x", "--json"}) }); err == nil {
			t.Fatalf("%s accepted", flag)
		}
	}
	if len(adapter.options) != 1 {
		t.Fatal("a rejected flag reached the surface", adapter.options)
	}
	if origin, found, err := app.Registry.SessionOrigin("forked"); err != nil || !found || origin.Via != registry.OriginPerson {
		t.Fatalf("a fork from a terminal is not recorded as the person's: origin=%+v found=%v err=%v", origin, found, err)
	}
}

type forkCLISurface struct {
	*cliSurface
	options []surface.ForkOptions
}

func (s *forkCLISurface) ForkSession(_ context.Context, session *surface.Session, options surface.ForkOptions) (*surface.Session, error) {
	s.options = append(s.options, options)
	fork := *session
	fork.ID = "forked"
	return &fork, nil
}
