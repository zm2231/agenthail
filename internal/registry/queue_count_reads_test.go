package registry

import (
	"testing"
	"time"
)

func TestQueueCountAndQueueCountsKeepOldPendingRows(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	r.now = func() time.Time { return time.Now().Add(-48 * time.Hour) }
	old, err := r.QueueMessageWithKey("session", "old", "old")
	if err != nil {
		t.Fatal(err)
	}
	r.now = time.Now
	if _, err := r.QueueMessageWithKey("session", "live", "live"); err != nil {
		t.Fatal(err)
	}

	if got := r.QueueCount("session"); got != 2 {
		t.Fatalf("QueueCount=%d, want both pending rows", got)
	}
	counts, err := r.QueueCounts()
	if err != nil || counts["session"] != 2 {
		t.Fatalf("QueueCounts=%v err=%v", counts, err)
	}
	row, err := r.QueueItem(old)
	if err != nil || row.Status != "pending" || row.LastError != "" || row.Historical {
		t.Fatalf("old row changed: row=%+v err=%v", row, err)
	}
}
