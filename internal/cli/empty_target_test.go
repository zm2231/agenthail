package cli

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnEmptySurfaceTargetNamesNoThreadEvenWhenOnlyOneExists(t *testing.T) {
	e := exitedClaudeApp(t)
	if _, err := captureStdout(t, func() error { return e.app.Run([]string{"send", e.session.ID, "hello"}) }); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"thread", "status", "claude:"},
		{"thread", "logs", "claude:"},
		{"thread", "stop", "claude:"},
		{"thread", "resume", "claude:"},
		{"last", "claude:"},
	} {
		if output, err := captureStdout(t, func() error { return e.app.Run(args) }); err == nil {
			t.Fatalf("%v acted on the only session: %q", args, output)
		}
	}
	if spawns := e.host.Spawns(t, terminalConversation); len(spawns) != 1 {
		t.Fatalf("an empty target stopped or resumed the only session: %+v", spawns)
	}
}

func TestThreadCreateFromAnEmptySurfaceTargetStartsNothing(t *testing.T) {
	starter := &starterCLISurface{
		cliSurface: &cliSurface{kind: surface.KindCodex},
		session:    &surface.Session{ID: "thread-new", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "managed"},
		delivery:   &surface.SendResult{UUID: "turn-new", Accepted: true},
	}
	app := threadFixture(t, starter)
	if err := app.Registry.RegisterSession(surface.Session{ID: "only-claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", t.TempDir(), "--from", "claude:"})
	})
	if err == nil || len(starter.options) != 0 {
		t.Fatalf("--from claude: started a thread attributed to the only session: err=%v output=%q", err, output)
	}
}
