package daemon

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAScanCancelsWorkQueuedForASessionNoOneReads(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	if err := r.EnsureSender(surface.CLISessionID); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(surface.CLISessionID, "[agenthail delivery failure id=1 target=gone] send failed"); err != nil {
		t.Fatal(err)
	}
	d.scanAndRelay(context.Background())
	if count := r.QueueCount(surface.CLISessionID); count != 0 {
		t.Fatalf("%d messages still queued for the Agenthail CLI", count)
	}
	watched, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range watched {
		if session.ID == surface.CLISessionID {
			t.Fatalf("the Agenthail CLI is still watched: %+v", watched)
		}
	}
}
