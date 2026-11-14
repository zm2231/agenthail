package registry

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func sendingToTarget(t *testing.T, r *Registry, message string) *DeliveryIntent {
	t.Helper()
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: message, Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func sentToTarget(t *testing.T, r *Registry, message, providerKey string) *DeliveryIntent {
	t.Helper()
	intent := sendingToTarget(t, r, message)
	if ok, err := r.MarkDeliveryIntentSent(intent.ID, providerKey, surface.EvidenceTransportAccepted); err != nil || !ok {
		t.Fatalf("sent=%v err=%v", ok, err)
	}
	return intent
}

func deliveryAskStatuses(t *testing.T, r *Registry) []string {
	t.Helper()
	var statuses []string
	for _, ask := range openDeliveryAsks(t, r) {
		statuses = append(statuses, ask.Ref[strings.LastIndex(ask.Ref, ":")+1:])
	}
	return statuses
}

func TestAHoldReportedWithoutTheDeliveryIdHoldsOnlyTheOneMessageItCanBe(t *testing.T) {
	cause := PeerHeldReason("no-mode-asserted")
	heldNow := func() time.Time { return time.Now().Add(time.Minute) }
	open := func(t *testing.T) *Registry {
		r := openTestRegistry(t)
		register(t, r, "sender")
		if err := r.RegisterSession(surface.Session{ID: "target", Name: "probe", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds"}); err != nil {
			t.Fatal(err)
		}
		return r
	}

	t.Run("one candidate is held", func(t *testing.T) {
		r := open(t)
		intent := sentToTarget(t, r, "only", "k1")
		if match, err := r.HoldPeerMessageFromSender("sender", "target", heldNow(), cause); err != nil || match != PeerHoldMatched {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != DeliveryIntentHeld || got.Failure != cause {
			t.Fatalf("intent=%+v err=%v", got, err)
		}
		if statuses := deliveryAskStatuses(t, r); len(statuses) != 1 || statuses[0] != deliveryHeld {
			t.Fatalf("problems=%v, want one hold", statuses)
		}
	})

	t.Run("a send still in flight is held and stays held once recorded", func(t *testing.T) {
		r := open(t)
		intent := sendingToTarget(t, r, "in flight")
		if match, err := r.HoldPeerMessageFromSender("sender", "target", heldNow(), cause); err != nil || match != PeerHoldMatched {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if ok, err := r.MarkDeliveryIntentSent(intent.ID, "k1", surface.EvidenceTransportAccepted); err != nil || !ok {
			t.Fatalf("sent=%v err=%v", ok, err)
		}
		if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != DeliveryIntentHeld || got.Failure != cause {
			t.Fatalf("intent=%+v err=%v", got, err)
		}
	})

	t.Run("several candidates may each have been held", func(t *testing.T) {
		r := open(t)
		first := sentToTarget(t, r, "first", "k1")
		second := sendingToTarget(t, r, "second")
		if match, err := r.HoldPeerMessageFromSender("sender", "target", heldNow(), cause); err != nil || match != PeerHoldAmbiguous {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if ok, err := r.MarkDeliveryIntentSent(second.ID, "k2", surface.EvidenceTransportAccepted); err != nil || !ok {
			t.Fatalf("sent=%v err=%v", ok, err)
		}
		for _, id := range []int64{first.ID, second.ID} {
			got, err := r.DeliveryIntent(id)
			if err != nil || got.Status != DeliveryIntentSent || !strings.Contains(got.Failure, "One of these 2 messages to probe may have been held") {
				t.Fatalf("intent=%+v err=%v, want sent and possibly held", got, err)
			}
		}
		if statuses := deliveryAskStatuses(t, r); len(statuses) != 2 || statuses[0] != deliveryMaybeHeld || statuses[1] != deliveryMaybeHeld {
			t.Fatalf("problems=%v, want two possible holds", statuses)
		}
		if ok, err := r.ReconcileDeliveryIntent("target", "k1"); err != nil || !ok {
			t.Fatalf("reconciled=%v err=%v", ok, err)
		}
		if got, err := r.DeliveryIntent(first.ID); err != nil || got.Status != DeliveryIntentDelivered || got.Failure != "" {
			t.Fatalf("delivered intent=%+v err=%v", got, err)
		}
		if statuses := deliveryAskStatuses(t, r); len(statuses) != 1 || statuses[0] != deliveryMaybeHeld {
			t.Fatalf("problems=%v, want only the undelivered one", statuses)
		}
	})

	t.Run("no candidate records nothing", func(t *testing.T) {
		r := open(t)
		if match, err := r.HoldPeerMessageFromSender("sender", "target", heldNow(), cause); err != nil || match != PeerHoldUnmatched {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if statuses := deliveryAskStatuses(t, r); len(statuses) != 0 {
			t.Fatalf("problems=%v", statuses)
		}
	})

	t.Run("a hold reported after the delivery resolved records nothing", func(t *testing.T) {
		r := open(t)
		intent := sentToTarget(t, r, "done", "k1")
		if ok, err := r.ReconcileDeliveryIntent("target", "k1"); err != nil || !ok {
			t.Fatalf("reconciled=%v err=%v", ok, err)
		}
		if match, err := r.HoldPeerMessageFromSender("sender", "target", heldNow(), cause); err != nil || match != PeerHoldUnmatched {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != DeliveryIntentDelivered {
			t.Fatalf("intent=%+v err=%v", got, err)
		}
		if statuses := deliveryAskStatuses(t, r); len(statuses) != 0 {
			t.Fatalf("problems=%v", statuses)
		}
	})

	t.Run("a hold never reaches a message sent later in the same second", func(t *testing.T) {
		r := open(t)
		heldAt := time.Now()
		time.Sleep(2 * time.Millisecond)
		intent := sentToTarget(t, r, "just after", "k1")
		if got, err := r.DeliveryIntent(intent.ID); err != nil || got.CreatedAt.Before(heldAt.Truncate(time.Second)) {
			t.Fatalf("intent=%+v err=%v", got, err)
		}
		if match, err := r.HoldPeerMessageFromSender("sender", "target", heldAt, cause); err != nil || match != PeerHoldUnmatched {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != DeliveryIntentSent || got.Failure != "" {
			t.Fatalf("intent=%+v err=%v", got, err)
		}
	})

	t.Run("a hold never reaches a message sent after it", func(t *testing.T) {
		r := open(t)
		intent := sentToTarget(t, r, "later", "k1")
		if match, err := r.HoldPeerMessageFromSender("sender", "target", time.Now().Add(-time.Hour), cause); err != nil || match != PeerHoldUnmatched {
			t.Fatalf("match=%v err=%v", match, err)
		}
		if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != DeliveryIntentSent || got.Failure != "" {
			t.Fatalf("intent=%+v err=%v", got, err)
		}
	})
}
