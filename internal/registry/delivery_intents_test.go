package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

func TestDeliveryProblemsReopenOrderAndBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, first, "sender", "target")
	for i := 0; i < 55; i++ {
		intent, recordErr := first.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: strings.Repeat("m", 20<<10), Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
		if recordErr != nil {
			first.Close()
			t.Fatal(recordErr)
		}
		status := DeliveryIntentFailed
		if i%2 == 0 {
			status = DeliveryIntentExpired
		}
		if changed, failErr := first.FailDeliveryIntent(intent.ID, status, fmt.Sprintf("reason-%d", i)); failErr != nil || !changed {
			first.Close()
			t.Fatalf("i=%d changed=%v err=%v", i, changed, failErr)
		}
		if _, err := first.db.Exec(`UPDATE delivery_intents SET updated_at=? WHERE id=?`, fmt.Sprintf("2026-01-%02d 00:00:00", (i%28)+1), intent.ID); err != nil {
			first.Close()
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	problems, err := second.ListDeliveryProblems()
	if err != nil || len(problems) != 50 {
		t.Fatalf("problems=%d err=%v", len(problems), err)
	}
	for i := 1; i < len(problems); i++ {
		if problems[i-1].At.Before(problems[i].At) {
			t.Fatalf("problems not newest first: %v before %v", problems[i-1].At, problems[i].At)
		}
	}
	if len(problems[0].Message) != 16<<10 || problems[0].Status != DeliveryIntentExpired && problems[0].Status != DeliveryIntentFailed {
		t.Fatalf("problem=%+v", problems[0])
	}
}

func TestDismissDeliveryProblemIsIdempotentAndAtomic(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.FailDeliveryIntent(intent.ID, DeliveryIntentFailed, "transport failed"); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := r.db.Exec(`CREATE TRIGGER reject_delivery_dismissed BEFORE INSERT ON catalog_events WHEN NEW.type='delivery.dismissed' BEGIN SELECT RAISE(ABORT, 'reject dismissal'); END`); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.DismissDeliveryProblem(intent.ID); err == nil || changed {
		t.Fatalf("atomic dismissal changed=%v err=%v", changed, err)
	}
	var dismissed sql.NullString
	if err := r.db.QueryRow(`SELECT dismissed_at FROM delivery_intents WHERE id=?`, intent.ID).Scan(&dismissed); err != nil || dismissed.Valid {
		t.Fatalf("rollback dismissed_at=%q err=%v", dismissed.String, err)
	}
	if _, err := r.db.Exec(`DROP TRIGGER reject_delivery_dismissed`); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.DismissDeliveryProblem(intent.ID); err != nil || !changed {
		t.Fatalf("dismiss changed=%v err=%v", changed, err)
	}
	if changed, err := r.DismissDeliveryProblem(intent.ID); err != nil || changed {
		t.Fatalf("repeat dismissal changed=%v err=%v", changed, err)
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil || len(problems) != 0 {
		t.Fatalf("visible problems=%+v err=%v", problems, err)
	}
	var events, queued int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM catalog_events WHERE type='delivery.dismissed' AND dedupe_key=?`, fmt.Sprintf("delivery.dismissed:%d", intent.ID)).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM message_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if events != 1 || queued != 0 {
		t.Fatalf("events=%d queued=%d", events, queued)
	}
	sent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.DismissDeliveryProblem(sent.ID); err == nil || changed {
		t.Fatalf("sent dismissal changed=%v err=%v", changed, err)
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

func TestQueuedRelayExpiryCreatesOneBoundIntentNoticeAndCatalogEventAcrossReplayAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, first, "sender", "target")
	options := surface.SendOptions{SourceSessionID: "sender"}
	queueID, err := first.QueueRelayMessageWithOptions("target", "relay body", "relay:7:turn-1", 1, options)
	if err != nil {
		t.Fatal(err)
	}
	deliveryID, err := queuedDeliveryIntentID(first.db, queueID)
	if err != nil || deliveryID == 0 {
		t.Fatalf("deliveryID=%d err=%v", deliveryID, err)
	}
	if replayID, err := first.QueueRelayMessageWithOptions("target", "relay body", "relay:7:turn-1", 1, options); err != nil || replayID != queueID {
		t.Fatalf("replayID=%d queueID=%d err=%v", replayID, queueID, err)
	}
	if _, err := first.db.Exec(`UPDATE message_queue SET expires_at_ms=1 WHERE id=?`, queueID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ExpireMessages(time.Now()); err != nil {
		t.Fatal(err)
	}
	intent, err := first.DeliveryIntent(deliveryID)
	if err != nil || intent.Status != DeliveryIntentExpired || intent.SenderSessionID != "sender" || intent.TargetSessionID != "target" || intent.NotificationQueueID == 0 {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	window, err := first.CatalogEventsAfter(0, 10)
	if err != nil || len(window.Events) != 1 || window.Events[0].Type != "delivery.problem" {
		t.Fatalf("events=%+v err=%v", window, err)
	}
	var payload struct {
		DeliveryID      int64  `json:"deliveryId"`
		SessionID       string `json:"sessionId"`
		SourceSessionID string `json:"sourceSessionId"`
	}
	if err := json.Unmarshal(window.Events[0].Payload, &payload); err != nil || payload.DeliveryID != deliveryID || payload.SessionID != "target" || payload.SourceSessionID != "sender" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if replayID, err := second.QueueRelayMessageWithOptions("target", "relay body", "relay:7:turn-1", 1, options); err != nil || replayID != queueID {
		t.Fatalf("reopen replayID=%d queueID=%d err=%v", replayID, queueID, err)
	}
	if _, err := second.ExpireMessages(time.Now()); err != nil {
		t.Fatal(err)
	}
	if count := second.QueueCount("sender"); count != 1 {
		t.Fatalf("sender notices=%d", count)
	}
	window, err = second.CatalogEventsAfter(0, 10)
	if err != nil || len(window.Events) != 1 || window.Events[0].Type != "delivery.problem" {
		t.Fatalf("reopened events=%+v err=%v", window, err)
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
