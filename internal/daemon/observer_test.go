package daemon

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestAScanCancelsWorkQueuedForASessionNoOneReads(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	if err := r.EnsureOperatorSession(); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(registry.OperatorSessionID, "[agenthail delivery failure id=1 target=gone] send failed"); err != nil {
		t.Fatal(err)
	}
	d.scanAndRelay(context.Background())
	if count := r.QueueCount(registry.OperatorSessionID); count != 0 {
		t.Fatalf("%d messages still queued for the operator", count)
	}
	watched, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range watched {
		if session.ID == registry.OperatorSessionID {
			t.Fatalf("the operator is still watched: %+v", watched)
		}
	}
}
