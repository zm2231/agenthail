package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeBackgroundCreationUsesReturnedIdentityAndLifecycle(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$HOME/argv"
if [ "$1" = agents ]; then
 printf '[{"id":"ab12cd34","sessionId":"real-session-id","kind":"background","name":"builder","cwd":"%s","state":"%s"}]\n' "$HOME" "$(cat "$HOME/state" 2>/dev/null || printf done)"
elif [ "$1" = --bg ]; then
 if [ "$2" = --resume ]; then
  printf working > "$HOME/state"
  mkdir -p "$HOME/.claude/jobs/ab12cd34"
  printf '{"state":"working","updatedAt":"2999-01-01T00:00:00Z"}' > "$HOME/.claude/jobs/ab12cd34/state.json"
 fi
 printf '\033[32mbackgrounded · ab12cd34\033[0m\n'
else
 printf 'operation complete\n'
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	c := NewClaude("Default", home)
	session, turn, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "--literal prompt", Cwd: home, Name: "builder", Worktree: "feature", TurnOptions: surface.TurnOptions{Effort: "high"}})
	if err != nil || session == nil || session.ID != "real-session-id" || turn != nil {
		t.Fatalf("session=%+v turn=%+v err=%v", session, turn, err)
	}
	for _, action := range []string{"status", "logs", "stop", "resume"} {
		if _, err := c.SessionAction(context.Background(), session, action); err != nil {
			t.Fatal(action, err)
		}
	}
	args, _ := os.ReadFile(filepath.Join(home, "argv"))
	if strings.Contains(string(args), "--session-id") || !strings.Contains(string(args), "--\n--literal prompt") || !strings.Contains(string(args), "--resume\nreal-session-id") {
		t.Fatalf("args=%s", args)
	}
	if _, err := c.SessionAction(context.Background(), &surface.Session{ID: "other"}, "stop"); err == nil {
		t.Fatal("unowned background session accepted")
	}
}

func TestClaudeMalformedBackgroundOutputIsUnknown(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'unexpected output\\n'\n"), 0700)
	t.Setenv("AGENTHAIL_CLAUDE_BIN", binary)
	session, _, err := NewClaude("", home).StartSession(context.Background(), surface.SessionStartOptions{Message: "hello", Cwd: home})
	if session != nil || !surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}
