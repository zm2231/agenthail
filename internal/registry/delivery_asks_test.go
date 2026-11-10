package registry

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func openDeliveryAsks(t *testing.T, r *Registry) []Ask {
	t.Helper()
	open, err := r.OpenAsks()
	if err != nil {
		t.Fatal(err)
	}
	var asks []Ask
	for _, ask := range open {
		if ask.Origin == AskOriginDelivery {
			asks = append(asks, ask)
		}
	}
	return asks
}

func onlyDeliveryAsk(t *testing.T, r *Registry) Ask {
	t.Helper()
	asks := openDeliveryAsks(t, r)
	if len(asks) != 1 {
		t.Fatalf("open delivery asks=%+v, want one", asks)
	}
	return asks[0]
}

func deliveryRef(t *testing.T, ask Ask) DeliveryRef {
	t.Helper()
	ref, ok := ParseDeliveryRef(ask.Ref)
	if !ok {
		t.Fatalf("ask %s has ref %q, not a delivery ref", ask.ID, ask.Ref)
	}
	return ref
}

func askOptions(ask Ask) []string {
	var ids []string
	for _, question := range ask.Questions {
		for _, option := range question.Options {
			ids = append(ids, option.ID)
		}
	}
	return ids
}

func askState(t *testing.T, r *Registry, id string) string {
	t.Helper()
	ask, found, err := r.Ask(id)
	if err != nil || !found {
		t.Fatalf("ask %s found=%v err=%v", id, found, err)
	}
	return ask.State
}

func TestAFailedSendFromAnAgentAsksThatAgent(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "please rebase", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, queueID)
	ask := onlyDeliveryAsk(t, r)
	if ask.SessionID != "sender" || ask.RoutedTo != "sender" || ask.Origin != AskOriginDelivery {
		t.Fatalf("ask=%+v, want it on the sender and routed to it", ask)
	}
	if ref := deliveryRef(t, ask); ref.IntentID != deliveryID || ref.Kind != "failed" {
		t.Fatalf("ref=%+v, want the failed intent %d", ref, deliveryID)
	}
	if found, ok, err := r.DeliveryAskOf(deliveryID); err != nil || !ok || found.ID != ask.ID {
		t.Fatalf("DeliveryAskOf=%+v found=%v err=%v", found, ok, err)
	}
	if count := r.QueueCount("sender"); count != 0 {
		t.Fatalf("the sender was queued %d notices besides its ask", count)
	}
}

func TestThePersonsSendAndARelayedMessageAskThePersonAboutTheTarget(t *testing.T) {
	for name, queue := range map[string]func(*Registry) (int64, error){
		"the person's send": func(r *Registry) (int64, error) {
			if err := r.EnsureSender(surface.PersonSessionID); err != nil {
				return 0, err
			}
			id, _, err := r.QueueDeliveryWithIntent("target", "from the person", "", surface.SendOptions{SourceSessionID: surface.PersonSessionID})
			return id, err
		},
		"a relayed message": func(r *Registry) (int64, error) {
			return r.QueueRelayMessageWithOptions("target", "relayed", "relay:1:turn-1", 1, surface.SendOptions{SourceSessionID: "sender"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := openTestRegistry(t)
			register(t, r, "sender", "target")
			queueID, err := queue(r)
			if err != nil {
				t.Fatal(err)
			}
			failQueued(t, r, queueID)
			ask := onlyDeliveryAsk(t, r)
			if ask.SessionID != "target" || ask.RoutedTo != AskToPerson {
				t.Fatalf("ask=%+v, want it on the target and routed to the person", ask)
			}
			if count := r.QueueCount("sender"); count != 0 {
				t.Fatalf("sender notices=%d", count)
			}
		})
	}
}

func TestADeadQueueRowWhoseIntentFailedRaisesOnlyTheIntentsAsk(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "rejected", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, queueID)
	if row, err := r.QueueItem(queueID, nil); err != nil || row.Status != "dead" {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if ref := deliveryRef(t, onlyDeliveryAsk(t, r)); ref.IntentID != deliveryID || ref.QueueID != 0 {
		t.Fatalf("ref=%+v, want only the intent's ask", ref)
	}
}

func TestAnUnconfirmedDeadRowAndARefusedRowRaiseQueueAsks(t *testing.T) {
	t.Run("unconfirmed", func(t *testing.T) {
		r := openTestRegistry(t)
		register(t, r, "sender", "target")
		queueID, _ := queueClaimed(t, r, "maybe delivered")
		if err := r.DeadLetterUnknown(queueID, errors.New("connection closed")); err != nil {
			t.Fatal(err)
		}
		ask := onlyDeliveryAsk(t, r)
		if ref := deliveryRef(t, ask); ref.QueueID != queueID || ref.Kind != "dead" || ask.Ref != fmt.Sprintf("queue:%d:dead", queueID) {
			t.Fatalf("ask ref=%q, want the dead queue row %d", ask.Ref, queueID)
		}
		if got := askOptions(ask); !reflect.DeepEqual(got, []string{DeliveryRetry, DeliveryDismiss}) {
			t.Fatalf("options=%v", got)
		}
	})
	t.Run("refused", func(t *testing.T) {
		r := openTestRegistry(t)
		register(t, r, "sender", "target")
		queueID, _ := queueClaimed(t, r, "start please")
		if err := r.DeferMessage(queueID, surface.DeliveryRefused(errors.New("Claude refused to resume target: Workspace not trusted")), time.Now()); err != nil {
			t.Fatal(err)
		}
		ask := onlyDeliveryAsk(t, r)
		if ref := deliveryRef(t, ask); ref.QueueID != queueID || ref.Kind != "refused" || ask.Ref != fmt.Sprintf("queue:%d:refused:1", queueID) {
			t.Fatalf("ask ref=%q, want the first refusal of queue row %d", ask.Ref, queueID)
		}
		if got := askOptions(ask); !reflect.DeepEqual(got, []string{DeliveryCancel, DeliveryDismiss}) {
			t.Fatalf("options=%v", got)
		}
		if ask.SessionID != "sender" || ask.RoutedTo != "sender" {
			t.Fatalf("ask=%+v, want it routed to the sender", ask)
		}
	})
}

func TestDeleteIsOfferedOnlyForADeletableFailureAndRetryOnlyWithADeadQueueRow(t *testing.T) {
	for name, test := range map[string]struct {
		fail func(*testing.T, *Registry) int64
		want []string
	}{
		"a queued send that went dead": {func(t *testing.T, r *Registry) int64 {
			queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "queued", "", surface.SendOptions{SourceSessionID: "sender"})
			if err != nil {
				t.Fatal(err)
			}
			failQueued(t, r, queueID)
			return deliveryID
		}, []string{DeliveryRetry, DeliveryDelete, DeliveryDismiss}},
		"a send refused before it left": {func(t *testing.T, r *Registry) int64 {
			return failIntent(t, r, DeliveryIntentSubmitted, surface.EvidenceSubmitted)
		}, []string{DeliveryDelete, DeliveryDismiss}},
		"a send the target accepted": {func(t *testing.T, r *Registry) int64 {
			return failIntent(t, r, DeliveryIntentSent, surface.EvidenceTransportAccepted)
		}, []string{DeliveryDismiss}},
	} {
		t.Run(name, func(t *testing.T) {
			r := openTestRegistry(t)
			register(t, r, "sender", "target")
			deliveryID := test.fail(t, r)
			ask, found, err := r.DeliveryAskOf(deliveryID)
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if got := askOptions(ask); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("options=%v, want %v", got, test.want)
			}
			deleteErr := r.DeleteFailedDelivery(deliveryID)
			if offered := contains(test.want, DeliveryDelete); offered != (deleteErr == nil) || !offered && !errors.Is(deleteErr, ErrDeliveryNotDeletable) {
				t.Fatalf("delete offered=%v but DeleteFailedDelivery err=%v", offered, deleteErr)
			}
		})
	}
}

func failIntent(t *testing.T, r *Registry, status DeliveryIntentStatus, evidence surface.DeliveryEvidence) int64 {
	t.Helper()
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: "direct", Status: status, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.RecordDeliveryFailure(intent.ID, "transport failed"); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	return intent.ID
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestALaterDeliveredMessageToTheSameTargetWithdrawsTheAsk(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target", "other")
	queueID, failedID, err := r.QueueDeliveryWithIntent("target", "first", "first:1", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, queueID)
	ask := onlyDeliveryAsk(t, r)
	if _, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "other", Status: DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted}); err != nil {
		t.Fatal(err)
	}
	if state := askState(t, r, ask.ID); state != AskOpen {
		t.Fatalf("a delivery to another target left the ask %s", state)
	}
	later, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if state := askState(t, r, ask.ID); state != AskOpen {
		t.Fatalf("a message not yet delivered left the ask %s", state)
	}
	if changed, err := r.MarkDeliveryIntentSent(later.ID, "", surface.EvidenceTransportAccepted); err != nil || !changed {
		t.Fatalf("mark sent changed=%v err=%v", changed, err)
	}
	if state := askState(t, r, ask.ID); state != AskWithdrawn {
		t.Fatalf("a later delivery to the same target left the ask %s", state)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("open asks=%+v", asks)
	}
	assertFailureStaysInHistory(t, r, failedID, queueID)
}

func TestAHeldMessageAsksUntilItsTargetReleasesOrDropsIt(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	hold := func(message string) (PeerMessage, *DeliveryIntent, Ask) {
		t.Helper()
		_, intent := queueClaimed(t, r, message)
		peer := PeerMessage{MsgID: intent.UUID, TargetSessionID: "target"}
		if held, err := r.HoldPeerMessage(peer, "inbound policy"); err != nil || !held {
			t.Fatalf("held=%v err=%v", held, err)
		}
		ask := onlyDeliveryAsk(t, r)
		if ref := deliveryRef(t, ask); ref.IntentID != intent.ID || ref.Kind != "held" || ask.RoutedTo != "sender" {
			t.Fatalf("ask=%+v, want the held intent %d routed to its sender", ask, intent.ID)
		}
		if got := askOptions(ask); !reflect.DeepEqual(got, []string{DeliveryDismiss}) {
			t.Fatalf("options=%v", got)
		}
		return peer, intent, ask
	}

	peer, _, released := hold("released later")
	if ok, err := r.ReleasePeerMessage(peer); err != nil || !ok {
		t.Fatalf("released=%v err=%v", ok, err)
	}
	if state := askState(t, r, released.ID); state != AskWithdrawn {
		t.Fatalf("a released message left its ask %s", state)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("open asks after release=%+v", asks)
	}

	peer, intent, dropped := hold("dropped later")
	if ok, err := r.DropPeerMessage(peer, "Claude dropped it"); err != nil || !ok {
		t.Fatalf("dropped=%v err=%v", ok, err)
	}
	if state := askState(t, r, dropped.ID); state != AskWithdrawn {
		t.Fatalf("a dropped message left its held ask %s", state)
	}
	failed := onlyDeliveryAsk(t, r)
	if ref := deliveryRef(t, failed); ref.IntentID != intent.ID || ref.Kind != "failed" {
		t.Fatalf("ref=%+v, want the dropped intent %d failed", ref, intent.ID)
	}
}

func TestRetryingAnAnsweredFailureAndFailingAgainOpensANewAsk(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, deliveryID, err := r.QueueDeliveryWithIntent("target", "retried body", "direct:retry", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	failQueued(t, r, queueID)
	first := onlyDeliveryAsk(t, r)
	if claimed, err := r.ClaimAsk(first.ID, AskAnswer{Answers: map[string]string{first.Questions[0].Question: "Retry"}}, "sender", "test", time.Now()); err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if err := r.SyncDeliveryAsks(time.Now()); err != nil {
		t.Fatal(err)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("an answered failure was asked again: %+v", asks)
	}
	if err := r.RetryMessage(queueID); err != nil {
		t.Fatal(err)
	}
	if intent, err := r.DeliveryIntent(deliveryID); err != nil || intent.Status != DeliveryIntentQueued || intent.Failure != "" || intent.Retries != 1 {
		t.Fatalf("retried intent=%+v err=%v", intent, err)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("a retried failure is still asked: %+v", asks)
	}

	failQueued(t, r, queueID)
	second := onlyDeliveryAsk(t, r)
	if second.ID == first.ID || second.Ref == first.Ref || deliveryRef(t, second).IntentID != deliveryID {
		t.Fatalf("second=%+v first=%+v, want a new ask for the new attempt", second, first)
	}
	if state := askState(t, r, first.ID); state != AskAnswered {
		t.Fatalf("the first attempt's ask is %s, want it to stay answered", state)
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
	if intent, err := r.DeliveryIntent(deliveryID); err != nil || intent.Status != DeliveryIntentSent || intent.Evidence != surface.EvidenceDelivered {
		t.Fatalf("redelivered intent=%+v err=%v", intent, err)
	}
	if state := askState(t, r, second.ID); state != AskWithdrawn {
		t.Fatalf("a redelivered message left its ask %s", state)
	}
}

func TestARestartedSessionTakingTheMessageWithdrawsItsAsk(t *testing.T) {
	for name, raise := range map[string]func(*testing.T, *Registry, *DeliveryIntent){
		"failed": func(t *testing.T, r *Registry, intent *DeliveryIntent) {
			if changed, err := r.RecordDeliveryFailure(intent.ID, "old conversation exited"); err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
		},
		"held": func(t *testing.T, r *Registry, intent *DeliveryIntent) {
			if held, err := r.HoldPeerMessage(PeerMessage{MsgID: intent.UUID, TargetSessionID: "target"}, "inbound policy"); err != nil || !held {
				t.Fatalf("held=%v err=%v", held, err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := openTestRegistry(t)
			register(t, r, "sender", "target", "restarted")
			queueID, intent := queueClaimed(t, r, "survives the restart")
			raise(t, r, intent)
			ask := onlyDeliveryAsk(t, r)
			if err := r.AckRestartedMessage(queueID, "target", "restarted", 0, surface.EvidenceDelivered, ""); err != nil {
				t.Fatal(err)
			}
			if state := askState(t, r, ask.ID); state != AskWithdrawn {
				t.Fatalf("the restarted session took the message but its ask is %s", state)
			}
		})
	}
}

func TestAProviderConfirmingAHeldMessageWithdrawsItsAsk(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", ProviderKey: "turn-1", Message: "hello", Status: DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.HoldPeerMessage(PeerMessage{MsgID: intent.UUID, TargetSessionID: "target"}, "held under its inbound policy"); err != nil || !changed {
		t.Fatalf("hold changed=%v err=%v", changed, err)
	}
	ask := onlyDeliveryAsk(t, r)
	if reconciled, err := r.ReconcileDeliveryIntent("target", "turn-1"); err != nil || !reconciled {
		t.Fatalf("reconciled=%v err=%v", reconciled, err)
	}
	if state := askState(t, r, ask.ID); state != AskWithdrawn {
		t.Fatalf("a held message the provider confirmed left its ask %s", state)
	}
}
