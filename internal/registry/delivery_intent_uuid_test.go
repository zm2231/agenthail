package registry

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestDeliveryIntentsCarryAUUIDFromBeforeTheyHadOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sender", "target"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []int64
	for _, message := range []string{"one", "two"} {
		intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: message, Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, intent.ID)
	}
	queueID, intentID, err := r.QueueDeliveryUsingIntent(ids[1], "two", "", surface.SendOptions{})
	if err != nil || intentID != ids[1] {
		t.Fatalf("queue = %d, %d, %v", queueID, intentID, err)
	}
	if _, err := r.db.Exec(`DROP INDEX delivery_intents_uuid`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE delivery_intents SET uuid=''; PRAGMA user_version=17`); err != nil {
		t.Fatal(err)
	}
	r.Close()

	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seen := map[string]bool{}
	for _, id := range ids {
		intent, err := r.DeliveryIntent(id)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := uuid.Parse(intent.UUID)
		if err != nil || parsed.Version() != 4 || seen[intent.UUID] {
			t.Fatalf("intent %d uuid %q: %v (duplicate %v)", id, intent.UUID, err, seen[intent.UUID])
		}
		seen[intent.UUID] = true
	}
	queued, err := r.DeliveryIntentUUIDForQueue(queueID)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := r.DeliveryIntent(ids[1])
	if queued != second.UUID {
		t.Fatalf("queued message uuid = %q, want its intent's %q", queued, second.UUID)
	}
	if none, err := r.DeliveryIntentUUIDForQueue(queueID + 100); err != nil || none != "" {
		t.Fatalf("uuid for a queue item without an intent = %q, %v", none, err)
	}
	if _, err := r.db.Exec(`UPDATE delivery_intents SET uuid=? WHERE id=?`, second.UUID, ids[0]); err == nil {
		t.Fatal("two intents share a uuid")
	}
}
