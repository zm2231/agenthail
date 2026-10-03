package registry

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestDeliveryIntentReconcilesOnlyByBoundProviderKey(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target", "other")
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", ProviderKey: "provider-request-1", Status: DeliveryIntentUnknown, Evidence: surface.EvidenceUnknown})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled, err := r.ReconcileDeliveryIntent("target", "unmatched-request"); err != nil || reconciled {
		t.Fatalf("unmatched reconciliation=%v err=%v", reconciled, err)
	}
	stored, err := r.DeliveryIntent(intent.ID)
	if err != nil || stored.Status != DeliveryIntentUnknown {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if reconciled, err := r.ReconcileDeliveryIntent("target", "provider-request-1"); err != nil || !reconciled {
		t.Fatalf("matching reconciliation=%v err=%v", reconciled, err)
	}
	stored, err = r.DeliveryIntent(intent.ID)
	if err != nil || stored.Status != DeliveryIntentDelivered || stored.Evidence != surface.EvidenceDelivered {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	if _, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "other", TargetSessionID: "target", ProviderKey: "provider-request-1", Status: DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted}); err == nil {
		t.Fatal("provider key was rebound to another sender")
	}
}

func TestDeliveryIntentFailureQueuesOneSenderNoticeAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, first, "sender", "target")
	intent, err := first.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := first.FailDeliveryIntent(intent.ID, DeliveryIntentExpired, "delivery queue expired"); err != nil || !changed {
		t.Fatalf("failure changed=%v err=%v", changed, err)
	}
	queueID, queued, err := first.QueueDeliveryFailureNotice(intent.ID)
	if err != nil || !queued || queueID == 0 {
		t.Fatalf("queueID=%d queued=%v err=%v", queueID, queued, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if duplicateID, queued, err := second.QueueDeliveryFailureNotice(intent.ID); err != nil || queued || duplicateID != queueID {
		t.Fatalf("duplicateID=%d queued=%v err=%v", duplicateID, queued, err)
	}
	if count := second.QueueCount("sender"); count != 1 {
		t.Fatalf("sender notice count=%d", count)
	}
	var events int
	if err := second.db.QueryRow(`SELECT COUNT(*) FROM catalog_events WHERE dedupe_key=? AND type='delivery.problem'`, "delivery.problem:"+fmt.Sprint(intent.ID)).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events=%d err=%v", events, err)
	}
}

func TestDeliveryIntentDoesNotNotifyUnknownOutcome(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentUnknown, Evidence: surface.EvidenceUnknown})
	if err != nil {
		t.Fatal(err)
	}
	if queueID, queued, err := r.QueueDeliveryFailureNotice(intent.ID); err != nil || queued || queueID != 0 {
		t.Fatalf("queueID=%d queued=%v err=%v", queueID, queued, err)
	}
	if count := r.QueueCount("sender"); count != 0 {
		t.Fatalf("unknown outcome queued %d notices", count)
	}
}

func TestQueueExpiryNotifiesBoundDeliveryIntentOnce(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, err := r.QueueMessageWithKey("target", "wait", "expiry-test")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: "wait", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BindDeliveryIntentQueue(intent.ID, queueID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE message_queue SET expires_at_ms=1 WHERE id=?`, queueID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExpireMessages(time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := r.DeliveryIntent(intent.ID)
	if err != nil || stored.Status != DeliveryIntentExpired || r.QueueCount("sender") != 1 {
		t.Fatalf("intent=%+v err=%v notices=%d", stored, err, r.QueueCount("sender"))
	}
}
