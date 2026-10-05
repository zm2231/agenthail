package surfaces

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestClaudeTranscriptIsFoundByConversationIDInAnyProject(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	worktree := writeClaudeProjectTranscript(t, home, "-Volumes-work-repo--worktrees-fix-a", "11111111-aaaa")
	hashed := writeClaudeProjectTranscript(t, home, "-Users-dev-very-long-name-abc123", "22222222-bbbb")
	if got := adapter.transcriptPath(&surface.Session{ID: "11111111-aaaa", Cwd: "/Volumes/work/repo/.worktrees/fix-a"}); got != worktree {
		t.Fatalf("worktree transcript %q, want %q", got, worktree)
	}
	if got := adapter.transcriptPath(&surface.Session{ID: "22222222-bbbb", Cwd: "/Users/dev/somewhere/else"}); got != hashed {
		t.Fatalf("transcript in a project not named after the cwd %q, want %q", got, hashed)
	}
}

func TestClaudeTranscriptAppearsAfterTheSessionStarts(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	writeClaudeProjectTranscript(t, home, "-Users-dev-app", "existing")
	if got := adapter.resolveTranscript("new-1"); got != "" {
		t.Fatalf("a conversation with no transcript resolved to %q", got)
	}
	dir := filepath.Join(home, ".claude", "projects", "-Users-dev-app")
	later := time.Now().Add(2 * time.Second)
	want := writeClaudeProjectTranscript(t, home, "-Users-dev-app", "new-1")
	if err := os.Chtimes(dir, later, later); err != nil {
		t.Fatal(err)
	}
	if got := adapter.resolveTranscript("new-1"); got != want {
		t.Fatalf("a transcript written after the first lookup resolved to %q, want %q", got, want)
	}
}

func TestClaudeTranscriptDoesNotGuessBetweenProjects(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	writeClaudeProjectTranscript(t, home, "-Users-dev-one", "dup-1")
	writeClaudeProjectTranscript(t, home, "-Users-dev-two", "dup-1")
	if got := adapter.resolveTranscript("dup-1"); got != "" {
		t.Fatalf("an id present in two projects resolved to %q", got)
	}
	if got := adapter.resolveTranscript("../escape"); got != "" {
		t.Fatalf("an id with a path separator resolved to %q", got)
	}
}

func TestClaudeTranscriptIndexRelistsOnlyChangedProjects(t *testing.T) {
	home := t.TempDir()
	index := newClaudeTranscriptIndex(home)
	for _, dir := range []string{"-a", "-b", "-c"} {
		writeClaudeProjectTranscript(t, home, dir, "seed"+dir)
	}
	index.pass()
	stale := filepath.Join(home, ".claude", "projects", "-b", "unindexed.jsonl")
	if err := os.WriteFile(stale, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := index.dirs["-b"].modified
	if err := os.Chtimes(filepath.Dir(stale), old, old); err != nil {
		t.Fatal(err)
	}
	if got := index.pass()("unindexed"); got != "" {
		t.Fatalf("a project whose modification time did not change was listed again: %q", got)
	}
	if err := os.RemoveAll(filepath.Join(home, ".claude", "projects", "-c")); err != nil {
		t.Fatal(err)
	}
	if got := index.pass()("seed-c"); got != "" {
		t.Fatalf("a removed project still resolved to %q", got)
	}
	if _, ok := index.dirs["-c"]; ok {
		t.Fatal("a removed project stayed in the index")
	}
}

func TestClaudeTranscriptPassListsProjectsOnce(t *testing.T) {
	home := t.TempDir()
	index := newClaudeTranscriptIndex(home)
	writeClaudeProjectTranscript(t, home, "-a", "seed")
	lookup := index.pass()
	created := writeClaudeProjectTranscript(t, home, "-new", "late")
	for _, id := range []string{"missing-1", "missing-2", "late"} {
		if got := lookup(id); got != "" {
			t.Fatalf("a lookup within one pass listed the projects again and found %q for %s", got, id)
		}
	}
	if got := index.pass()("late"); got != created {
		t.Fatalf("the next pass resolved %q, want %q", got, created)
	}
}

func TestClaudeTranscriptBecomesAmbiguousAndThenUniqueAgain(t *testing.T) {
	home := t.TempDir()
	adapter := NewClaude("", home)
	first := writeClaudeProjectTranscript(t, home, "-Users-dev-one", "conv-1")
	if got := adapter.resolveTranscript("conv-1"); got != first {
		t.Fatalf("unique transcript resolved to %q, want %q", got, first)
	}
	second := writeClaudeProjectTranscript(t, home, "-Users-dev-two", "conv-1")
	if got := adapter.resolveTranscript("conv-1"); got != "" {
		t.Fatalf("a transcript that appeared in a second project resolved to %q", got)
	}
	project := filepath.Dir(first)
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(project, later, later); err != nil {
		t.Fatal(err)
	}
	if got := adapter.resolveTranscript("conv-1"); got != second {
		t.Fatalf("after the duplicate was removed the transcript resolved to %q, want %q", got, second)
	}
}
