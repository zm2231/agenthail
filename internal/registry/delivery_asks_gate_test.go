package registry

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestASettledFailedSendNoLongerMakesTheDeliverySyncWaitForTheWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	register(t, r, "sender", "target")
	queueID, _, err := r.QueueDeliveryWithIntent("target", "first", "first:1", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, queueID)
	later, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.MarkDeliveryIntentSent(later.ID, "", surface.EvidenceTransportAccepted); err != nil || !changed {
		t.Fatalf("mark sent changed=%v err=%v", changed, err)
	}
	if _, err := r.db.Exec(`UPDATE message_queue SET dead_at_ms=0 WHERE id=?`, queueID); err != nil {
		t.Fatal(err)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("open delivery asks=%+v, want none once a later send reached the target", asks)
	}

	writer, err := sql.Open("sqlite", path+"?_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	held, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	synced := make(chan error, 1)
	go func() { synced <- r.SyncDeliveryAsks(time.Now()) }()
	select {
	case err := <-synced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the delivery sync waited for another writer although nothing needed asking or withdrawing")
	}
}

func TestAnUpgradedRegistryAnswersWhetherDeliveryAsksAreInPlayFromIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP INDEX delivery_intents_sent_failure`,
		`DROP INDEX message_queue_dead`,
		`DELETE FROM schema_migrations WHERE name='2026-11-22-indexed-delivery-asks-gate'`,
	} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	now := time.Now()
	plan := queryPlan(t, r, deliveryAsksInPlay, now.Add(-deadReviewWindow).UnixMilli(), now.Add(-deliveryLateAfter).UnixMilli())
	for _, step := range plan {
		if (strings.Contains(step, "delivery_intents") || strings.Contains(step, "message_queue")) && !strings.Contains(step, " INDEX ") {
			t.Errorf("deciding whether delivery asks are in play reads a whole table: %q", step)
		}
	}
	for _, index := range []string{"delivery_intents_sent_failure", "message_queue_dead"} {
		if !strings.Contains(strings.Join(plan, "\n"), index) {
			t.Errorf("deciding whether delivery asks are in play does not use %s:\n%s", index, strings.Join(plan, "\n"))
		}
	}
}
