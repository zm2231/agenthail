package surfaces

import (
	"os"
	"os/exec"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeRunsOnlyWhileItsRecordedProcessHoldsTheConversation(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	record := map[string]any{"pid": os.Getpid(), "sessionId": "conversation", "procStart": testProcessStart(t), "version": "2.1.303"}
	writePeerRecord(t, home, os.Getpid(), record)
	exited := exec.Command("true")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		session surface.Session
		running bool
	}{
		{"its process", surface.Session{ID: "conversation", Surface: surface.KindClaude, PID: os.Getpid()}, true},
		{"held by another process", surface.Session{ID: "conversation", Surface: surface.KindClaude, PID: exited.Process.Pid, HolderPID: os.Getpid()}, true},
		{"process now runs another conversation", surface.Session{ID: "earlier", Surface: surface.KindClaude, PID: os.Getpid()}, false},
		{"process exited", surface.Session{ID: "conversation", Surface: surface.KindClaude, PID: exited.Process.Pid}, false},
	} {
		if got := claude.RuntimeRunning(&tc.session); got != tc.running {
			t.Errorf("%s: running=%v, want %v", tc.name, got, tc.running)
		}
	}
	record["procStart"] = "1"
	writePeerRecord(t, home, os.Getpid(), record)
	if claude.RuntimeRunning(&surface.Session{ID: "conversation", Surface: surface.KindClaude, PID: os.Getpid()}) {
		t.Error("a record left by an exited process whose pid was reused counts as running")
	}
}
