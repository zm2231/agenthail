package registry

import (
	"errors"
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

func TestQueuedRelayFailureRaisesOneAskForThePersonWithoutNotifyingTheRelaySourceAcrossReplayAndReopen(t *testing.T) {
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
	ask := onlyDeliveryAsk(t, first)
	if ask.SessionID != "target" || ask.RoutedTo != AskToPerson {
		t.Fatalf("ask=%+v, want it on the target and routed to the person", ask)
	}
	intent, err := first.DeliveryIntent(deliveryRef(t, ask).IntentID)
	if err != nil || intent.QueueID != queueID || intent.Status != DeliveryIntentFailed || intent.SenderSessionID != "sender" || intent.TargetSessionID != "target" || !intent.Relayed {
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
	if err := second.SyncDeliveryAsks(time.Now()); err != nil {
		t.Fatal(err)
	}
	if again := onlyDeliveryAsk(t, second); again.ID != ask.ID {
		t.Fatalf("reopened ask=%+v, want %s", again, ask.ID)
	}
}

func TestQueueDeliveryFromAnUnregisteredSenderIsRecordedAsAgenthail(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "wait", "", surface.SendOptions{SourceSessionID: "unregistered"})
	if err != nil || queueID == 0 || deliveryID == 0 {
		t.Fatalf("queueID=%d deliveryID=%d err=%v", queueID, deliveryID, err)
	}
	intent, err := r.DeliveryIntent(deliveryID)
	if err != nil || intent.SenderSessionID != surface.SystemSessionID || intent.QueueID != queueID {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestQueueTerminalFailureFailsTheIntentAndAsksItsSender(t *testing.T) {
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
	if err := r.DeadLetterMessage(item.ID, errors.New("target rejected input")); err != nil {
		t.Fatal(err)
	}
	stored, err := r.DeliveryIntent(deliveryID)
	if err != nil || stored.Status != DeliveryIntentFailed || stored.Failure != "target rejected input" {
		t.Fatalf("intent=%+v err=%v", stored, err)
	}
	if ask, found, err := r.DeliveryAskOf(deliveryID); err != nil || !found || ask.RoutedTo != "sender" || !strings.Contains(ask.Questions[0].Question, "target rejected input") {
		t.Fatalf("ask=%+v found=%v err=%v", ask, found, err)
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
	if err != nil || stored.Status != DeliveryIntentUnknown || stored.Evidence != surface.EvidenceUnknown {
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
	if err := r.DeadLetterMessage(queueID, errors.New("delivery rejected [target missing]: gone")); err != nil {
		t.Fatal(err)
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
	ask := onlyDeliveryAsk(t, r)
	if ask.SessionID != "target" || ask.RoutedTo != AskToPerson {
		t.Fatalf("ask=%+v, want it on the target and routed to the person", ask)
	}
	intent, err := r.DeliveryIntent(deliveryRef(t, ask).IntentID)
	if err != nil || intent.QueueID != queueID || !intent.Relayed || intent.SenderSessionID != "source" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestASenderNothingReadsGetsNoNoticeAndItsAsksGoToThePerson(t *testing.T) {
	senders := map[string]func(*Registry) (string, error){
		"unrepliable": func(r *Registry) (string, error) {
			return r.EnsureUnrepliableSender("h0123456789ab", "worker-1", "vps/builder")
		},
		"person": func(r *Registry) (string, error) {
			return surface.PersonSessionID, r.EnsureSender(surface.PersonSessionID)
		},
		"cli": func(r *Registry) (string, error) { return surface.CLISessionID, r.EnsureSender(surface.CLISessionID) },
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
			if err := r.DeferMessage(queueID, surface.DeliveryRefused(errors.New("Claude refused to resume target: Workspace not trusted")), now); err != nil {
				t.Fatal(err)
			}
			if count := r.QueueCount(sender); count != 0 {
				t.Fatalf("%d refusal notices queued for a sender nothing reads", count)
			}
			if refused := onlyDeliveryAsk(t, r); refused.SessionID != "target" || refused.RoutedTo != AskToPerson || deliveryRef(t, refused).QueueID != queueID {
				t.Fatalf("refusal ask=%+v, want it on the target and routed to the person", refused)
			}
			if _, err := r.RecordDeliveryFailure(deliveryID, "transport failed"); err != nil {
				t.Fatal(err)
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
			failed := onlyDeliveryAsk(t, r)
			if failed.SessionID != "target" || failed.RoutedTo != AskToPerson || deliveryRef(t, failed).IntentID != deliveryID {
				t.Fatalf("failure ask=%+v, want it on the target and routed to the person", failed)
			}
		})
	}
}

func TestAFailedDeliveryStopsAskingAfterADayButAHeldOneDoesNot(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target", "other")
	oldQueue, oldID, err := r.QueueDeliveryWithIntent("target", "old", "old:1", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, oldQueue)
	recentQueue, recentID, err := r.QueueDeliveryWithIntent("target", "recent", "recent:1", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, recentQueue)
	held, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "other", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE delivery_intents SET status=? WHERE id=?`, DeliveryIntentHeld, held.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE delivery_intents SET updated_at=datetime('now','-25 hours') WHERE id IN (?,?)`, oldID, held.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncDeliveryAsks(time.Now()); err != nil {
		t.Fatal(err)
	}
	asks := openDeliveryAsks(t, r)
	ids := map[int64]bool{}
	for _, ask := range asks {
		ids[deliveryRef(t, ask).IntentID] = true
	}
	if len(asks) != 2 || !ids[recentID] || !ids[held.ID] {
		t.Fatalf("asks = %+v", asks)
	}
	assertFailureStaysInHistory(t, r, oldID, oldQueue)
}

func assertFailureStaysInHistory(t *testing.T, r *Registry, deliveryID, queueID int64) {
	t.Helper()
	intent, err := r.DeliveryIntent(deliveryID)
	if err != nil || intent.Status != DeliveryIntentFailed {
		t.Fatalf("intent = %+v, %v", intent, err)
	}
	queued, err := r.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range queued {
		if item.ID == queueID {
			return
		}
	}
	t.Fatalf("queue list --all lost the failed message %d: %+v", queueID, queued)
}
