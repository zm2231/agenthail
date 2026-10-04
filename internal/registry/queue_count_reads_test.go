package registry

import (
	"testing"
	"time"
)

func TestQueueCountAndQueueCountsFilterExpiredPendingRowsWithoutWriting(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	expired, err := r.QueueMessageWithKey("session", "expired", "expired")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithKey("session", "live", "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE message_queue SET expires_at_ms=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), expired); err != nil {
		t.Fatal(err)
	}

	if got := r.QueueCount("session"); got != 1 {
		t.Fatalf("QueueCount=%d, want only live pending row", got)
	}
	counts, err := r.QueueCounts()
	if err != nil || counts["session"] != 1 {
		t.Fatalf("QueueCounts=%v err=%v", counts, err)
	}
	var status string
	var lastError string
	if err := r.db.QueryRow(`SELECT status,last_error FROM message_queue WHERE id=?`, expired).Scan(&status, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || lastError != "" {
		t.Fatalf("expired row was mutated: status=%q lastError=%q", status, lastError)
	}
}
