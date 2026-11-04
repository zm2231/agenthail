package surfaces

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEndedConversationNeedsTheRootToSayTheTranscriptIsGone(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "work")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(project, "kept.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude(nil, root)
	for name, test := range map[string]struct {
		path string
		want bool
	}{
		"on disk":          {transcript, true},
		"never recorded":   {"", false},
		"deleted":          {filepath.Join(project, "gone.jsonl"), false},
		"root not mounted": {filepath.Join(root, "unplugged", "projects", "work", "gone.jsonl"), true},
	} {
		if got, _ := claude.EndedConversation(test.path); got != test.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if _, changed := claude.EndedConversation(transcript); changed.IsZero() {
		t.Error("a transcript on disk reports when it changed")
	}
}
