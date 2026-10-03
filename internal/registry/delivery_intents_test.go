package registry

import (
	"errors"
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
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "wait", "expiry-test", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil || deliveryID == 0 {
		t.Fatalf("deliveryID=%d err=%v", deliveryID, err)
	}
	if _, err := r.db.Exec(`UPDATE message_queue SET expires_at_ms=1 WHERE id=?`, queueID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ExpireMessages(time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != DeliveryIntentExpired || stored.QueueID != queueID || r.QueueCount("sender") != 1 {
		t.Fatalf("intent=%+v err=%v notices=%d", stored, err, r.QueueCount("sender"))
	}
	if _, err := r.ExpireMessages(time.Now()); err != nil {
		t.Fatal(err)
	}
	if count := r.QueueCount("sender"); count != 1 {
		t.Fatalf("sender notice count=%d", count)
	}
}

func TestQueueDeliveryWithIntentSkipsUnregisteredSender(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "wait", "", surface.SendOptions{SourceSessionID: "unregistered"})
	if err != nil || queueID == 0 || deliveryID != 0 {
		t.Fatalf("queueID=%d deliveryID=%d err=%v", queueID, deliveryID, err)
	}
}

func TestQueueTerminalFailureNotifiesSenderWithDeadLetter(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	_, deliveryID, err := r.QueueDeliveryWithIntent("target", "wait", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	notified, err := r.DeadLetterMessage(item.ID, errors.New("target rejected input"))
	if err != nil || !notified {
		t.Fatalf("notified=%v err=%v", notified, err)
	}
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != DeliveryIntentFailed || stored.NotificationQueueID == 0 || stored.Failure != "target rejected input" {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
	if count := r.QueueCount("sender"); count != 1 {
		t.Fatalf("sender notice count=%d", count)
	}
}

func TestQueuedDeliveryAckBindsProviderKeyForReconciliation(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	_, deliveryID, err := r.QueueDeliveryWithIntent("target", "wait", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.AckMessageWithEvidence(item.ID, "target", 0, surface.EvidenceDelivered, "turn-7"); err != nil {
		t.Fatal(err)
	}
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != DeliveryIntentSent || stored.ProviderKey != "turn-7" || stored.Evidence != surface.EvidenceDelivered {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
	if reconciled, err := r.ReconcileDeliveryIntent("target", "turn-7"); err != nil || !reconciled {
		t.Fatalf("reconciled=%v err=%v", reconciled, err)
	}
}

func TestQueuedDeliveryUnknownOutcomeRetainsUncertaintyWithoutNotice(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	_, deliveryID, err := r.QueueDeliveryWithIntent("target", "wait", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.DeadLetterUnknown(item.ID, errors.New("connection closed")); err != nil {
		t.Fatal(err)
	}
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != DeliveryIntentUnknown || stored.Evidence != surface.EvidenceUnknown || stored.NotificationQueueID != 0 {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
	if count := r.QueueCount("sender"); count != 0 {
		t.Fatalf("unknown outcome queued %d notices", count)
	}
}
