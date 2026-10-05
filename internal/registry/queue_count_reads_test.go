package registry

import (
	"testing"
	"time"
)

func TestQueueCountAndQueueCountsFilterExpiredPendingRowsWithoutWriting(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	r.now = func() time.Time { return time.Now().Add(-2 * queueMessageTTL) }
	expired, err := r.QueueMessageWithKey("session", "expired", "expired")
	if err != nil {
		t.Fatal(err)
	}
	r.now = time.Now
	if _, err := r.QueueMessageWithKey("session", "live", "live"); err != nil {
		t.Fatal(err)
	}

	if got := r.QueueCount("session"); got != 1 {
		t.Fatalf("QueueCount=%d, want only live pending row", got)
	}
	counts, err := r.QueueCounts()
	if err != nil || counts["session"] != 1 {
		t.Fatalf("QueueCounts=%v err=%v", counts, err)
	}
	row, err := r.QueueItem(expired)
	if err != nil || row.Status != "pending" || row.LastError != "" {
		t.Fatalf("expired row was mutated: row=%+v err=%v", row, err)
	}
}
