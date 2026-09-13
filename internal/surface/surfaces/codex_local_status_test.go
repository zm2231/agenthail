package surfaces

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexTranscriptStatusTracksLiveTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	started := `{"type":"event_msg","payload":{"type":"task_started"}}` + "\n"
	if err := os.WriteFile(path, []byte(started+strings.Repeat(`{"type":"response_item"}`+"\n", 4000)), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if got := codexTranscriptStatus(path, now, now); got != surface.StatusBusy {
		t.Fatalf("status = %q, want busy", got)
	}
	if err := os.WriteFile(path, []byte(started+`{"type":"event_msg","payload":{"type":"task_complete"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := codexTranscriptStatus(path, now, now); got != surface.StatusIdle {
		t.Fatalf("status = %q, want idle", got)
	}
}

func TestCodexTranscriptStatusReconstructsTaskEventStraddlingChunkBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	task := `{"type":"event_msg","payload":{"type":"task_started"}}`
	resp := `{"type":"response_item","payload":{"n":0}}` + "\n"
	now := time.Now()

	for delta := int64(-60); delta <= 5; delta++ {
		var trailing strings.Builder
		for int64(trailing.Len()) < codexStatusReadChunk+delta {
			trailing.WriteString(resp)
		}
		content := "leadingpartialjunk\n" + task + "\n" + trailing.String()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if got := codexTranscriptStatus(path, now, now); got != surface.StatusBusy {
			t.Fatalf("delta=%d size=%d status=%q, want busy", delta, len(content), got)
		}
	}
}

func TestCodexTranscriptStatusRejectsStaleUnfinishedTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"task_started"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if got := codexTranscriptStatus(path, now.Add(-codexBusyFreshness-time.Second), now); got != surface.StatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
}

func TestClaudePeerIdleRequiresCapability(t *testing.T) {
	withoutIdleSignal := map[string]any{"status": "idle"}
	if got := claudePeerStatus(withoutIdleSignal); got != surface.StatusUnknown {
		t.Fatalf("status = %q, want unknown", got)
	}
	withIdleSignal := map[string]any{"status": "idle", "peerFeatures": []any{"notify_idle"}}
	if got := claudePeerStatus(withIdleSignal); got != surface.StatusIdle {
		t.Fatalf("status = %q, want idle", got)
	}
	if got := claudePeerStatus(map[string]any{"status": "busy"}); got != surface.StatusBusy {
		t.Fatalf("status = %q, want busy", got)
	}
}
