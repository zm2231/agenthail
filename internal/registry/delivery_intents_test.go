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
		if changed, failErr := first.FailDeliveryIntent(intent.ID, fmt.Sprintf("reason-%d", i)); failErr != nil || !changed {
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
	if len(problems[0].Message) != 16<<10 || problems[0].Status != DeliveryIntentFailed {
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
	if changed, err := r.FailDeliveryIntent(intent.ID, "transport failed"); err != nil || !changed {
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
	queued, err := r.ListQueue(true, nil)
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
	if changed, err := first.FailDeliveryIntent(intent.ID, "delivery rejected"); err != nil || !changed {
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

func TestQueuedRelayFailureRecordsOneProblemWithoutNotifyingTheRelaySourceAcrossReplayAndReopen(t *testing.T) {
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
	failQueued(t, first, queueID)
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
	if err != nil || intent.QueueID != queueID || intent.Status != DeliveryIntentFailed || intent.SenderSessionID != "sender" || intent.TargetSessionID != "target" || !intent.Relayed || intent.NotificationQueueID != 0 {
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
	if count := second.QueueCount("sender"); count != 0 {
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

// failQueued records a terminal delivery rejection for one queued row.
func failQueued(t *testing.T, r *Registry, queueID int64) {
	t.Helper()
	if _, err := r.db.Exec(`UPDATE message_queue SET status='inflight',attempts=attempts+1 WHERE id=? AND status='pending'`, queueID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeadLetterMessage(queueID, errors.New("delivery rejected [target missing]: gone")); err != nil {
		t.Fatal(err)
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

func TestQueuedDirectSendFailureNotifiesTheSender(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "direct body", "direct:1", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil || queueID == 0 || deliveryID == 0 {
		t.Fatalf("queueID=%d deliveryID=%d err=%v", queueID, deliveryID, err)
	}
	failQueued(t, r, queueID)
	intent, err := r.DeliveryIntent(deliveryID)
	if err != nil || intent.Relayed || intent.Status != DeliveryIntentFailed || intent.NotificationQueueID == 0 {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	if count := r.QueueCount("sender"); count != 1 {
		t.Fatalf("sender notices=%d", count)
	}
}

func TestDroppedRelayRecordsAProblemWithoutNotifyingTheRelaySource(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	recorded, err := r.RecordReplyForwardFailure("sender", "target", "relay:7:turn-1", "relay body", "target session is no longer active")
	if err != nil || !recorded {
		t.Fatalf("recorded=%v err=%v", recorded, err)
	}
	if again, err := r.RecordReplyForwardFailure("sender", "target", "relay:7:turn-1", "relay body", "target session is no longer active"); err != nil || again {
		t.Fatalf("replay recorded=%v err=%v", again, err)
	}
	if count := r.QueueCount("sender"); count != 0 {
		t.Fatalf("sender notices=%d", count)
	}
	window, err := r.CatalogEventsAfter(0, 10)
	if err != nil || len(catalogEventsOfType(window, "delivery.problem")) != 1 {
		t.Fatalf("events=%+v err=%v", window, err)
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil || len(problems) != 1 || problems[0].SessionID != "target" {
		t.Fatalf("problems=%+v err=%v", problems, err)
	}
}

func TestFailedPersistedRelayCompletionDoesNotNotifyTheRelaySource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "source", "target")
	if _, err := r.AddRoute("source", "target", ".*"); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "turn-1", Events: []surface.Completion{{ID: "turn-1", Text: "relay body"}}}); err != nil {
		t.Fatal(err)
	}
	routes, err := r.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	events, err := r.PendingRelayEvents(routes[0])
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	queueID, err := r.ConsumeRelay(routes[0], events[0], "[agenthail relay hops=1] relay body", "queue", "")
	if err != nil || queueID == 0 {
		t.Fatalf("queueID=%d err=%v", queueID, err)
	}
	failQueued(t, r, queueID)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.CaptureRelayCompletions("source", "turn-1", surface.CompletionBatch{Cursor: "turn-1b", Events: []surface.Completion{{ID: "turn-1", Text: "relay body"}}}); err != nil {
		t.Fatal(err)
	}
	if count := r.QueueCount("source"); count != 0 {
		t.Fatalf("source notices=%d", count)
	}
	routes, err = r.ListRoutes()
	if err != nil || routes[0].PendingCount != 0 || routes[0].FireCount != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil || len(problems) != 1 {
		t.Fatalf("problems=%+v err=%v", problems, err)
	}
	intent, err := r.DeliveryIntent(problems[0].DeliveryID)
	if err != nil || intent.QueueID != queueID || !intent.Relayed || intent.NotificationQueueID != 0 || intent.SenderSessionID != "source" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	window, err := r.CatalogEventsAfter(0, 20)
	if err != nil || len(catalogEventsOfType(window, "delivery.problem")) != 1 {
		t.Fatalf("events=%+v err=%v", window, err)
	}
}

func TestDeliveryProblemFlagsTheSenderUntilItIsTold(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	if err := r.EnsureOperatorSession(); err != nil {
		t.Fatal(err)
	}
	attention := func(id int64) (string, bool) {
		t.Helper()
		problems, err := r.ListDeliveryProblems()
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range problems {
			if problem.DeliveryID == id {
				return problem.AttentionSessionID, true
			}
		}
		return "", false
	}
	agent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FailDeliveryIntent(agent.ID, "transport failed"); err != nil {
		t.Fatal(err)
	}
	if flagged, listed := attention(agent.ID); !listed || flagged != "sender" {
		t.Fatalf("failed agent send flagged %q listed=%v, want the sender", flagged, listed)
	}
	noticeID, _, err := r.QueueDeliveryFailureNotice(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := r.ClaimNextMessage("sender", time.Now())
	if err != nil || claimed == nil || claimed.ID != noticeID {
		t.Fatalf("claimed=%+v notice=%d err=%v", claimed, noticeID, err)
	}
	if flagged, _ := attention(agent.ID); flagged != "sender" {
		t.Fatalf("sender flag cleared before the notice landed: %q", flagged)
	}
	if err := r.AckMessage(noticeID); err != nil {
		t.Fatal(err)
	}
	if flagged, listed := attention(agent.ID); !listed || flagged != "" {
		t.Fatalf("told sender still flagged %q listed=%v", flagged, listed)
	}
	person, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: OperatorSessionID, TargetSessionID: "target", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FailDeliveryIntent(person.ID, "transport failed"); err != nil {
		t.Fatal(err)
	}
	if flagged, listed := attention(person.ID); !listed || flagged != "" {
		t.Fatalf("person's failed send flagged %q listed=%v, want listed without a session", flagged, listed)
	}
}

func TestRetriedFailedDeliveryIsANewAttempt(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "retried body", "direct:retry", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, queueID)
	if err := r.RetryMessage(queueID); err != nil {
		t.Fatal(err)
	}
	intent, err := r.DeliveryIntent(deliveryID)
	if err != nil || intent.Status != DeliveryIntentQueued || intent.Failure != "" || intent.NotificationQueueID != 0 {
		t.Fatalf("retried intent=%+v err=%v", intent, err)
	}
	if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
		t.Fatalf("a retried failure is still a problem: %+v err=%v", problems, err)
	}
	window, err := r.CatalogEventsAfter(0, 50)
	if err != nil || len(catalogEventsOfType(window, "delivery.dismissed")) != 1 {
		t.Fatalf("events=%+v err=%v", window, err)
	}

	failQueued(t, r, queueID)
	intent, err = r.DeliveryIntent(deliveryID)
	if err != nil || intent.Status != DeliveryIntentFailed || intent.NotificationQueueID == 0 {
		t.Fatalf("refailed intent=%+v err=%v", intent, err)
	}
	if count := r.QueueCount("sender"); count != 2 {
		t.Fatalf("sender notices=%d, want one per failed attempt", count)
	}
	window, err = r.CatalogEventsAfter(0, 50)
	if err != nil || len(catalogEventsOfType(window, "delivery.problem")) != 2 {
		t.Fatalf("events=%+v err=%v", window, err)
	}

	if err := r.RetryMessage(queueID); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || item == nil || item.ID != queueID {
		t.Fatalf("claim=%+v err=%v want queue %d", item, err, queueID)
	}
	if err := r.AckMessageWithEvidence(queueID, "target", 0, surface.EvidenceDelivered, ""); err != nil {
		t.Fatal(err)
	}
	if intent, err = r.DeliveryIntent(deliveryID); err != nil || intent.Status != DeliveryIntentSent || intent.Evidence != surface.EvidenceDelivered {
		t.Fatalf("redelivered intent=%+v err=%v", intent, err)
	}
}

func TestNoNoticeIsQueuedForASenderNothingReads(t *testing.T) {
	senders := map[string]func(*Registry) (string, error){
		"unrepliable": func(r *Registry) (string, error) {
			return r.EnsureUnrepliableSender("h0123456789ab", "worker-1", "vps/builder")
		},
		"operator": func(r *Registry) (string, error) { return OperatorSessionID, r.EnsureOperatorSession() },
	}
	for name, ensure := range senders {
		t.Run(name, func(t *testing.T) {
			r := openTestRegistry(t)
			sender, err := ensure(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.RegisterSession(surface.Session{ID: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
				t.Fatal(err)
			}
			if err := r.SaveRuntimeState("target", surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
				t.Fatal(err)
			}
			queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "please rerun the build", "unrepliable-sender", surface.SendOptions{SourceSessionID: sender})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().Add(time.Hour)
			if item, err := r.ClaimNextMessage("target", now); err != nil || item == nil || item.ID != queueID {
				t.Fatalf("claim=%+v err=%v want queue %d", item, err, queueID)
			}
			if _, err := r.DeferMessage(queueID, surface.DeliveryRefused(errors.New("Claude refused to resume target: Workspace not trusted")), now); err != nil {
				t.Fatal(err)
			}
			if count := r.QueueCount(sender); count != 0 {
				t.Fatalf("%d refusal notices queued for a sender nothing reads", count)
			}
			if _, err := r.FailDeliveryIntent(deliveryID, "transport failed"); err != nil {
				t.Fatal(err)
			}
			if noticeID, _, err := r.QueueDeliveryFailureNotice(deliveryID); err != nil || noticeID != 0 {
				t.Fatalf("failure notice id=%d err=%v", noticeID, err)
			}
			if count := r.QueueCount(sender); count != 0 {
				t.Fatalf("%d notices queued for a sender nothing reads", count)
			}
			watched, err := r.WatchedSessions()
			if err != nil {
				t.Fatal(err)
			}
			for _, session := range watched {
				if session.ID == sender {
					t.Fatalf("the sender is watched: %+v", watched)
				}
			}
			problems, err := r.ListDeliveryProblems()
			if err != nil {
				t.Fatal(err)
			}
			for _, problem := range problems {
				if problem.DeliveryID == deliveryID {
					if problem.AttentionSessionID != "" {
						t.Fatalf("flagged %q for its failed send", problem.AttentionSessionID)
					}
					return
				}
			}
			t.Fatal("the failed send is not listed as a problem")
		})
	}
}
