package registry

import (
	"fmt"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaimingQueuedMessagesLeavesDeliveryAsksToThePassSync(t *testing.T) {
	r := openTestRegistry(t)
	const sessions = 5
	for i := range sessions {
		id := fmt.Sprintf("target-%d", i)
		register(t, r, id)
		if _, err := r.QueueMessageWithKey(id, "hello", "k-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`INSERT OR IGNORE INTO sessions(id,surface) VALUES(?, 'person')`, surface.PersonSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO delivery_intents(sender_session_id,target_session_id,message,status,evidence) VALUES(?,?,'held back','held','')`, surface.PersonSessionID, "target-0"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := range sessions {
		item, err := r.ClaimNextMessage(fmt.Sprintf("target-%d", i), now)
		if err != nil || item == nil {
			t.Fatalf("claim target-%d: item=%v err=%v", i, item, err)
		}
	}
	if open, err := r.OpenAsks(); err != nil || len(open) != 0 {
		t.Fatalf("%d claims synced delivery asks themselves: open=%d err=%v", sessions, len(open), err)
	}
	if err := r.SyncDeliveryAsks(now); err != nil {
		t.Fatal(err)
	}
	if open, err := r.OpenAsks(); err != nil || len(open) != 1 {
		t.Fatalf("one pass sync raised %d asks for the held delivery, err=%v", len(open), err)
	}
}
