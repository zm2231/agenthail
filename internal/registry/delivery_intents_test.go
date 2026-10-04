package registry

import (
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
	var failed []int64
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
		failed = append(failed, intent.ID)
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
	for i, problem := range problems {
		if want := failed[len(failed)-1-i]; problem.DeliveryID != want {
			t.Fatalf("problem %d delivery=%d, want newest-first delivery %d", i, problem.DeliveryID, want)
		}
		if i > 0 && problems[i-1].At.Before(problem.At) {
			t.Fatalf("problems not newest first: %v before %v", problems[i-1].At, problem.At)
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
	if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 1 || problems[0].DeliveryID != intent.ID {
		t.Fatalf("failed dismissal hid the problem: problems=%+v err=%v", problems, err)
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
	window, err := r.CatalogEventsAfter(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := r.ListQueue(true)
	if err != nil {
		t.Fatal(err)
	}
	if events := catalogEventsOfType(window, "delivery.dismissed"); len(events) != 1 || events[0].EntityID == "" || len(queued) != 0 {
		t.Fatalf("dismissed events=%+v queued=%+v", events, queued)
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
	window, err := second.CatalogEventsAfter(0, 50)
	if err != nil || len(catalogEventsOfType(window, "delivery.problem")) != 1 {
		t.Fatalf("events=%+v err=%v", window, err)
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
	if replayID, err := first.QueueRelayMessageWithOptions("target", "relay body", "relay:7:turn-1", 1, options); err != nil || replayID != queueID {
		t.Fatalf("replayID=%d queueID=%d err=%v", replayID, queueID, err)
	}
	afterTTL := time.Now().Add(2 * queueMessageTTL)
	if expired, err := first.ExpireMessages(afterTTL); err != nil || expired != 1 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}
	window, err := first.CatalogEventsAfter(0, 10)
	problems := catalogEventsOfType(window, "delivery.problem")
	if err != nil || len(problems) != 1 {
		t.Fatalf("events=%+v err=%v", window, err)
	}
	var payload struct {
		DeliveryID      int64  `json:"deliveryId"`
		SessionID       string `json:"sessionId"`
		SourceSessionID string `json:"sourceSessionId"`
	}
	if err := json.Unmarshal(problems[0].Payload, &payload); err != nil || payload.DeliveryID == 0 || payload.SessionID != "target" || payload.SourceSessionID != "sender" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
	intent, err := first.DeliveryIntent(payload.DeliveryID)
	if err != nil || intent.QueueID != queueID || intent.Status != DeliveryIntentExpired || intent.SenderSessionID != "sender" || intent.TargetSessionID != "target" || intent.NotificationQueueID == 0 {
		t.Fatalf("intent=%+v err=%v", intent, err)
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
	if err != nil || len(catalogEventsOfType(window, "delivery.problem")) != 1 {
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

func catalogEventsOfType(window CatalogEventWindow, kind string) []CatalogEvent {
	var events []CatalogEvent
	for _, event := range window.Events {
		if event.Type == kind {
			events = append(events, event)
		}
	}
	return events
}
