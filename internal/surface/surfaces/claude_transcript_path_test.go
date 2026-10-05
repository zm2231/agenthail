package surfaces

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func writeClaudeProjectTranscript(t *testing.T, home, dir, id string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", dir, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeTranscriptUsesClaudeCodeProjectDirectoryNames(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	cases := []struct{ cwd, dir string }{
		{"/Volumes/work/repo/.worktrees/fix-a", "-Volumes-work-repo--worktrees-fix-a"},
		{"/Users/dev/My Project/app_v2", "-Users-dev-My-Project-app-v2"},
		{"/Users/dev/.claude/skills/voice", "-Users-dev--claude-skills-voice"},
	}
	for index, test := range cases {
		id := []string{"11111111-aaaa", "22222222-bbbb", "33333333-cccc"}[index]
		want := writeClaudeProjectTranscript(t, home, test.dir, id)
		if got := adapter.transcriptPath(&surface.Session{ID: id, Cwd: test.cwd}); got != want {
			t.Fatalf("cwd %q: transcript %q, want %q", test.cwd, got, want)
		}
	}
}

func TestClaudeTranscriptFollowsAConversationThatChangedDirectory(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	want := writeClaudeProjectTranscript(t, home, "-Users-dev-launch", "moved-1")
	if got := adapter.transcriptPath(&surface.Session{ID: "moved-1", Cwd: "/Users/dev/launch/sub"}); got != want {
		t.Fatalf("moved conversation transcript %q, want %q", got, want)
	}
}

func TestClaudeTranscriptDoesNotGuessBetweenProjects(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	writeClaudeProjectTranscript(t, home, "-Users-dev-one", "dup-1")
	writeClaudeProjectTranscript(t, home, "-Users-dev-two", "dup-1")
	want := filepath.Join(home, ".claude", "projects", "-Users-dev-three", "dup-1.jsonl")
	if got := adapter.transcriptPath(&surface.Session{ID: "dup-1", Cwd: "/Users/dev/three"}); got != want {
		t.Fatalf("ambiguous id resolved to %q, want the launch-directory path %q", got, want)
	}
	if got := adapter.transcriptPath(&surface.Session{ID: "../escape", Cwd: "/Users/dev/three"}); got != "" {
		t.Fatalf("an id with a path separator resolved to %q", got)
	}
}
