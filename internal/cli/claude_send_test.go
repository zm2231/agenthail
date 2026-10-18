package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func sendToRegisteredClaudeJob(t *testing.T, state string) error {
	t.Helper()
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
if [ "$1" = agents ]; then
 printf '[{"id":"job12345","sessionId":"fixture-session","kind":"background","name":"fixture","cwd":"%s","state":"%s"}]\n' "$HOME" "$(cat "$HOME/state")"
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = surfaces.NewClaude("", home)
	if err := r.RegisterSession(surface.Session{ID: "fixture-session", Surface: surface.KindClaude, Cwd: home}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("worker", "fixture-session"); err != nil {
		t.Fatal(err)
	}
	return app.Run([]string{"send", "@worker", "hello"})
}

func TestSendToRegisteredClaudeSessionThatIsNotRunning(t *testing.T) {
	err := sendToRegisteredClaudeJob(t, "failed")
	if err == nil {
		t.Fatal("send to a session that is not running succeeded")
	}
	for _, want := range []string{"is not running", "job12345 is failed", "agenthail thread resume worker"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%v, missing %q", err, want)
		}
	}
}

func TestSendToRegisteredClaudeSessionThatIsNotReachableYet(t *testing.T) {
	err := sendToRegisteredClaudeJob(t, "working")
	if err == nil || !strings.Contains(err.Error(), "not reachable yet") || !strings.Contains(err.Error(), "job12345 is working") || strings.Contains(err.Error(), "resume") {
		t.Fatalf("err=%v", err)
	}
}
