package claudehost

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWaitExitedTellsAClaudeFromALaterProcessWithItsPID(t *testing.T) {
	started, err := processStart(os.Getpid())
	if err != nil || started == "" {
		t.Fatalf("start of this process = %q, %v", started, err)
	}
	if err := waitExited(context.Background(), os.Getpid(), "Thu Jan  1 00:00:00 1970"); err != nil {
		t.Fatalf("waiting on a pid now used by a later process = %v, want done", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := waitExited(ctx, os.Getpid(), started); err == nil {
		t.Fatal("waiting on a process that still runs returned done")
	}
}
