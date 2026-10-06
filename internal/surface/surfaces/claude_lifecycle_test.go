package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func fakeBackgroundClaude(t *testing.T, initialState, onResume string) (string, *Claude) {
	t.Helper()
	home := t.TempDir()
	script := `#!/bin/sh
env >> "$HOME/env"
if [ "$1" = agents ]; then
 printf '[{"id":"job12345","sessionId":"fixture-session","kind":"background","name":"fixture","cwd":"%s","state":"%s"}]\n' "$HOME" "$(cat "$HOME/state")"
elif [ "$1" = --bg ] && [ "$2" = --resume ]; then
 ` + onResume + `
 printf 'backgrounded · job12345 (idle)\n'
elif [ "$1" = --bg ]; then
 printf 'backgrounded · job12345\n'
fi
`
	binary := filepath.Join(home, "claude")
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "state"), []byte(initialState), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	return home, NewClaude("", home)
}

func TestClaudeBackgroundCommandsDropCmuxTerminalState(t *testing.T) {
	t.Setenv("CMUX_SURFACE_ID", "fixture-surface")
	t.Setenv("CMUX_SOCKET_PATH", "/nonexistent/fixture.sock")
	t.Setenv("CMUX_CLAUDE_HOOKS_DISABLED", "0")
	t.Setenv("AGENTHAIL_FIXTURE_KEEP", "kept")
	home, c := fakeBackgroundClaude(t, "working", "")
	if _, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: home}); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(filepath.Join(home, "env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(env), "\n") {
		if strings.HasPrefix(line, "CMUX_") {
			t.Fatalf("Claude inherited terminal state %q", line)
		}
	}
	if !strings.Contains(string(env), "AGENTHAIL_FIXTURE_KEEP=kept\n") || !strings.Contains(string(env), "HOME="+home+"\n") {
		t.Fatalf("env=%s", env)
	}
}

func TestClaudeResumeReportsJobThatFailsToStart(t *testing.T) {
	home, c := fakeBackgroundClaude(t, "stopped", `printf failed > "$HOME/state"`)
	jobDir := filepath.Join(home, ".claude", "jobs", "job12345")
	if err := os.MkdirAll(jobDir, 0700); err != nil {
		t.Fatal(err)
	}
	detail := "exit 1 before init: Error: Settings file not found: /fixture/settings.json"
	if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(`{"state":"failed","detail":"`+detail+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: "fixture-session"}, "resume")
	if err == nil || !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), detail) || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("result=%v err=%v", result, err)
	}
}

func TestClaudeResumeWaitsForRunningJob(t *testing.T) {
	_, c := fakeBackgroundClaude(t, "stopped", `printf starting > "$HOME/state"; (sleep 0.3; printf working > "$HOME/state") &`)
	result, err := c.SessionAction(context.Background(), &surface.Session{ID: "fixture-session"}, "resume")
	if err != nil || result["state"] != "working" || result["id"] != "job12345" {
		t.Fatalf("result=%v err=%v", result, err)
	}
}
