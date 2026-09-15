package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexTranscriptTailUsesNewestCompleteExchangeAndSkipsPartialJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"type":"event_msg","payload":{"type":"user_message","message":"older"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"older answer"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"newest"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"newest answer"}]}}
{"type":"response_item"`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	exchanges, err := codexTranscriptTail(context.Background(), path, 1)
	if err != nil || len(exchanges) != 1 || exchanges[0].User != "newest" || exchanges[0].Assistant != "newest answer" || exchanges[0].Source != "local-transcript" {
		t.Fatalf("exchanges=%+v err=%v", exchanges, err)
	}
}

func TestCodexTranscriptTailRequiresLocalTranscript(t *testing.T) {
	if _, err := codexTranscriptTail(context.Background(), "", 1); err == nil {
		t.Fatal("missing transcript accepted")
	}
}

func TestCodexTranscriptTailDoesNotDuplicateMirroredUserMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	data := `{"timestamp":"2026-09-15T00:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Continue"}}
{"timestamp":"2026-09-15T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}
{"timestamp":"2026-09-15T00:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	exchanges, err := codexTranscriptTail(context.Background(), path, 2)
	if err != nil || len(exchanges) != 1 || exchanges[0].User != "Continue" || exchanges[0].Assistant != "Done" {
		t.Fatalf("exchanges=%+v err=%v", exchanges, err)
	}
}
