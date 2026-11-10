package surfaces

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUnseenTranscript(t *testing.T, root, project, id string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func turn(entrypoint, extra string) string {
	return `{"type":"user","entrypoint":"` + entrypoint + `","cwd":"/work/app","sessionId":"x","isSidechain":false` + extra + `,"message":{"role":"user","content":"fix the parser"}}`
}

func TestUnseenConversationsListOnlyInteractiveOnesAgenthailDoesNotKnow(t *testing.T) {
	home, second := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+second+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := `{"type":"queue-operation","content":"` + strings.Repeat("a", 200<<10) + `"}`
	late := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("b", 150<<10) + `"},"entrypoint":"cli","cwd":"/work/late","isSidechain":false}`
	writeUnseenTranscript(t, root, "app", "terminal", `{"type":"last-prompt"}`, turn("cli", ""))
	writeUnseenTranscript(t, root, "app", "desktop", turn("claude-desktop", ""))
	writeUnseenTranscript(t, root, "app", "after-a-long-prompt", huge, late)
	writeUnseenTranscript(t, second, "app", "other-root", turn("cli", ""))
	writeUnseenTranscript(t, root, "app", "scripted", turn("sdk-cli", ""))
	writeUnseenTranscript(t, root, "app", "sdk-python", turn("sdk-py", ""))
	writeUnseenTranscript(t, root, "app", "sdk-typescript", turn("sdk-ts", ""))
	writeUnseenTranscript(t, root, "app", "stub", `{"type":"bridge-session"}`)
	writeUnseenTranscript(t, root, "app", "side", strings.Replace(turn("cli", ""), `"isSidechain":false`, `"isSidechain":true`, 1))
	writeUnseenTranscript(t, root, "app", "known", turn("cli", ""))
	writeUnseenTranscript(t, root, "app", "twice", turn("cli", ""))
	writeUnseenTranscript(t, root, "elsewhere", "twice", turn("cli", ""))

	claude := NewClaude(nil, home)
	got := map[string]string{}
	for _, session := range claude.UnseenConversations(func(id string) bool { return id == "known" }) {
		if session.Status != "offline" || !session.Ended || session.Transcript == "" || session.LastActive.IsZero() {
			t.Fatalf("%s is %+v, want an ended offline row with its transcript and time", session.ID, session)
		}
		got[session.ID] = session.Cwd
	}
	want := map[string]string{"terminal": "/work/app", "desktop": "/work/app", "after-a-long-prompt": "/work/late", "other-root": "/work/app"}
	if len(got) != len(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for id, cwd := range want {
		if got[id] != cwd {
			t.Fatalf("listed %v, want %v", got, want)
		}
	}
}

func TestAnEntrypointPastAnOversizedRecordOrOnAnUnterminatedLastLineIsFound(t *testing.T) {
	dir := t.TempDir()
	oversized := filepath.Join(dir, "oversized.jsonl")
	huge := `{"type":"user","entrypoint":"sdk-cli","content":"` + strings.Repeat("c", claudeEntrypointLineLimit) + `"}`
	if err := os.WriteFile(oversized, []byte(huge+"\n"+turn("cli", "")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unterminated := filepath.Join(dir, "unterminated.jsonl")
	if err := os.WriteFile(unterminated, []byte(`{"type":"queue-operation"}`+"\n"+turn("claude-desktop", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	entrypoints := newClaudeEntrypoints()
	for path, want := range map[string]string{oversized: "cli", unterminated: "claude-desktop"} {
		if got := entrypoints.read(path); got.kind != want || got.cwd != "/work/app" {
			t.Errorf("%s: got %+v, want %s", filepath.Base(path), got, want)
		}
	}
}

func TestUnseenConversationsCanBeAskedForConcurrently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	for _, id := range []string{"a", "b", "c", "d"} {
		writeUnseenTranscript(t, root, "app", id, turn("cli", ""))
	}
	claude := NewClaude(nil, home)
	counts := make(chan int, 8)
	for range 8 {
		go func() { counts <- len(claude.UnseenConversations(func(string) bool { return false })) }()
	}
	for range 8 {
		if got := <-counts; got != 4 {
			t.Fatalf("a concurrent pass listed %d, want 4", got)
		}
	}
}

type countingOpens struct{ count int }

func (c *countingOpens) open(path string) (io.ReadCloser, error) {
	c.count++
	return os.Open(path)
}

func TestATranscriptsEntrypointIsReadOnceAndOneWithoutItAgainOnlyWhenItChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	classified := writeUnseenTranscript(t, root, "app", "classified", turn("cli", ""))
	pending := writeUnseenTranscript(t, root, "app", "pending", `{"type":"queue-operation"}`)
	claude := NewClaude(nil, home)
	opens := &countingOpens{}
	claude.entrypoints.open = opens.open
	none := func(string) bool { return false }
	claude.UnseenConversations(none)
	claude.UnseenConversations(none)
	if opens.count != 2 {
		t.Fatalf("two passes opened transcripts %d times, want each once", opens.count)
	}
	if err := os.WriteFile(pending, []byte(`{"type":"queue-operation"}`+"\n"+turn("cli", "")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(classified, []byte(turn("cli", "")+"\n"+turn("cli", "")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listed := claude.UnseenConversations(none)
	if opens.count != 3 || len(listed) != 2 {
		t.Fatalf("after both transcripts grew: opens=%d listed=%d, want only the pending one read again and both listed", opens.count, len(listed))
	}
}
