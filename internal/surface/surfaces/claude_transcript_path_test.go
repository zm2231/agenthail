package surfaces

import (
	"os"
	"path/filepath"
	"strings"
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

func TestClaudeTranscriptIsNotPredictedForLongProjectNames(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	long := "/Users/dev/" + strings.Repeat("deep/", 50) + "repo"
	session := &surface.Session{ID: "long-1", Cwd: long}
	if got := adapter.transcriptPath(session); got != "" {
		t.Fatalf("a long project name was predicted as %q; Claude Code shortens it with a hash", got)
	}
	want := writeClaudeProjectTranscript(t, home, claudeProjectDir(long)[:claudeProjectDirLimit]+"-abc123", "long-1")
	if got := adapter.transcriptPath(session); got != want {
		t.Fatalf("long project transcript %q, want %q once it exists", got, want)
	}
}

func TestClaudeTranscriptLocatorListsProjectsOncePerPass(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	want := writeClaudeProjectTranscript(t, home, "-Users-dev-launch", "moved-2")
	locator := &claudeTranscriptLocator{projects: filepath.Join(home, ".claude", "projects")}
	for _, id := range []string{"pending-a", "pending-b", "pending-c"} {
		adapter.resolveTranscript(&surface.Session{Cwd: "/Users/dev/new"}, id, locator)
	}
	writeClaudeProjectTranscript(t, home, "-Users-dev-later", "pending-d")
	if got := adapter.resolveTranscript(&surface.Session{Cwd: "/Users/dev/new"}, "pending-d", locator); got != filepath.Join(home, ".claude", "projects", "-Users-dev-new", "pending-d.jsonl") {
		t.Fatalf("a project created after the pass listed projects was enumerated again: %q", got)
	}
	if got := adapter.resolveTranscript(&surface.Session{Cwd: "/Users/dev/launch/sub"}, "moved-2", locator); got != want {
		t.Fatalf("moved conversation %q, want %q", got, want)
	}
	if got := adapter.cachedTranscript("moved-2"); got != want {
		t.Fatalf("a located transcript was not remembered: %q", got)
	}
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	if got := adapter.cachedTranscript("moved-2"); got != "" {
		t.Fatalf("a remembered transcript that was deleted is still returned: %q", got)
	}
}
