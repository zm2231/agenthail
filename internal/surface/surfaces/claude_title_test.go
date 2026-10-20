package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTitleTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptTitleIsTheLastTitlePastOversizedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("x", 3*1024*1024) + `"}}`
	writeTitleTranscript(t, path,
		`{"type":"custom-title","customTitle":"first"}`,
		huge,
		`{"type":"assistant","message":{"content":"mentions \"custom-title\" in text"}}`,
		`not json "custom-title"`,
		`{"type":"custom-title","customTitle":"   "}`,
		`{"type":"custom-title","customTitle":"renamed"}`,
		huge,
	)
	if title := claudeTranscriptTitle(context.Background(), path); title != "renamed" {
		t.Fatalf("title=%q", title)
	}
	if title := claudeTranscriptTitle(context.Background(), filepath.Join(t.TempDir(), "missing.jsonl")); title != "" {
		t.Fatalf("missing transcript title=%q", title)
	}
	untitled := filepath.Join(t.TempDir(), "untitled.jsonl")
	writeTitleTranscript(t, untitled, `{"type":"user","message":{"content":"hi"}}`)
	if title := claudeTranscriptTitle(context.Background(), untitled); title != "" {
		t.Fatalf("untitled transcript title=%q", title)
	}
}

func TestConversationTitleReadsTheRootThatHoldsTheConversation(t *testing.T) {
	home := t.TempDir()
	const conversation = "3c1f7a52-6d0e-4b8a-9f21-5e7d8c9b0a14"
	second := filepath.Join(home, "claude-work")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+second+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	writeTitleTranscript(t, filepath.Join(second, "projects", "-work", conversation+".jsonl"), `{"type":"custom-title","customTitle":"second root"}`)
	c := NewClaude(nil, home)
	root, err := c.conversationRoot(conversation)
	if err != nil || root != second {
		t.Fatalf("root=%q err=%v", root, err)
	}
	if title := c.conversationTitle(context.Background(), conversation, root); title != "second root" {
		t.Fatalf("title=%q", title)
	}
}
