package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
)

func configureClaudeRoots(t *testing.T, home string, roots ...string) {
	t.Helper()
	quoted := make([]string, len(roots))
	for i, root := range roots {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		quoted[i] = `"` + root + `"`
	}
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":[`+strings.Join(quoted, ",")+`]}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeHostedCreationRunsUnderTheChosenRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	second := filepath.Join(host.Home, ".work", ".claude")
	configureClaudeRoots(t, host.Home, second)
	c := NewClaude(nil, host.Home)
	ctx := context.Background()

	session, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd, ConfigRoot: "work"})
	if err != nil {
		t.Fatal(err)
	}
	spawns := host.Spawns(t, session.ID)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != second {
		t.Fatalf("spawns=%+v, want one start in %s", spawns, second)
	}

	primary, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd, ConfigRoot: "default"})
	if err != nil {
		t.Fatal(err)
	}
	inherited, set := os.LookupEnv("CLAUDE_CONFIG_DIR")
	if spawns := host.Spawns(t, primary.ID); len(spawns) != 1 || (spawns[0].ConfigDir != nil) != set || set && *spawns[0].ConfigDir != inherited {
		t.Fatalf("spawns=%+v, want the primary root left to the host's own CLAUDE_CONFIG_DIR", spawns)
	}

	if _, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd, ConfigRoot: "nobody"}); err == nil || !strings.Contains(err.Error(), "no configured Claude config directory") {
		t.Fatalf("an unknown config root started: %v", err)
	}
}

func TestClaudeBackgroundCreationRunsUnderTheChosenRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	second := filepath.Join(home, "spare")
	configureClaudeRoots(t, home, second)
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
printf '%s %s\n' "$1" "${CLAUDE_CONFIG_DIR-unset}" >> "$HOME/config-dirs"
if [ "$1" = agents ]; then
 printf '[{"id":"ab12cd34","sessionId":"real-session-id","kind":"background","name":"builder","cwd":"%s","state":"working"}]\n' "$HOME"
else
 printf 'backgrounded · ab12cd34\n'
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	session, _, err := NewClaude(nil, home).BackgroundStarter().StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: home, ConfigRoot: second})
	if err != nil || session == nil || session.ID != "real-session-id" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	data, _ := os.ReadFile(filepath.Join(home, "config-dirs"))
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.HasSuffix(line, " "+second) {
			t.Fatalf("claude ran outside the chosen root: %s", data)
		}
	}
}

func TestCodexCreationRefusesAClaudeConfigRoot(t *testing.T) {
	c := &Codex{}
	if _, _, err := c.startSessionOnTransport(context.Background(), nil, surface.SessionStartOptions{Message: "hi", ConfigRoot: "work"}, codexTransportManaged); err == nil || !strings.Contains(err.Error(), "require Claude") {
		t.Fatalf("err=%v", err)
	}
}
