package registry

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func inboxItemContaining(t *testing.T, r *Registry, sessionID, text string) InboxItem {
	t.Helper()
	for message, item := range inboxMessages(t, r, sessionID) {
		if strings.Contains(message, text) {
			return item
		}
	}
	t.Fatalf("no inbox item for %s contains %q", sessionID, text)
	return InboxItem{}
}

func assertSettledInInbox(t *testing.T, item InboxItem, queueStatus string, evidence surface.DeliveryEvidence) {
	t.Helper()
	if item.Pending() || item.QueueStatus != queueStatus || item.Evidence != evidence {
		t.Fatalf("pending=%v queueStatus=%q evidence=%s, want settled as %q with %s: %+v", item.Pending(), item.QueueStatus, item.Evidence, queueStatus, evidence, item)
	}
}

func queueClaimed(t *testing.T, r *Registry, message string) (int64, *DeliveryIntent) {
	t.Helper()
	queueID, intentID, err := r.QueueDeliveryWithIntent("target", message, "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if item, err := r.ClaimNextMessage("target", time.Now()); err != nil || item == nil || item.ID != queueID {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	intent, err := r.DeliveryIntent(intentID)
	if err != nil {
		t.Fatal(err)
	}
	return queueID, intent
}

func TestACanceledQueuedMessageIsNotPendingInTheInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	id, _, err := r.QueueDeliveryWithIntent("target", "cancel me", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CancelMessage(id); err != nil {
		t.Fatal(err)
	}
	assertSettledInInbox(t, inboxItemContaining(t, r, "target", "cancel me"), "canceled", surface.EvidenceCanceled)
}

func TestMessagesCanceledForASessionAreNotPendingInTheInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	for _, message := range []string{"first", "second"} {
		if _, _, err := r.QueueDeliveryWithIntent("target", message, "", surface.SendOptions{SourceSessionID: "sender"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := r.CancelMessagesForSession("target"); err != nil || n != 2 {
		t.Fatalf("canceled=%d err=%v", n, err)
	}
	for _, message := range []string{"first", "second"} {
		assertSettledInInbox(t, inboxItemContaining(t, r, "target", message), "canceled", surface.EvidenceCanceled)
	}
}

func TestAMessageCanceledByItsKeyIsNotPendingInTheInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	if _, err := r.QueueMessageWithKey("target", "keyed notice", "ledger:readback:7"); err != nil {
		t.Fatal(err)
	}
	if canceled, err := r.CancelPendingByKey("ledger:readback:7"); err != nil || !canceled {
		t.Fatalf("canceled=%v err=%v", canceled, err)
	}
	assertSettledInInbox(t, inboxItemContaining(t, r, "target", "keyed notice"), "canceled", surface.EvidenceCanceled)
}

func TestAHeldNoticeCanceledByAPeerReleaseIsNotPendingInTheInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	_, intent := queueClaimed(t, r, "parked by peer policy")
	peer := PeerMessage{MsgID: intent.UUID, TargetSessionID: "target"}
	if held, err := r.HoldPeerMessage(peer, "Claude held it."+peerHoldExpires); err != nil || !held {
		t.Fatalf("held=%v err=%v", held, err)
	}
	if notice := inboxItemContaining(t, r, "target", "held a message to you"); !notice.Pending() {
		t.Fatalf("the held notice to the target is not pending: %+v", notice)
	}
	if released, err := r.ReleasePeerMessage(peer); err != nil || !released {
		t.Fatalf("released=%v err=%v", released, err)
	}
	assertSettledInInbox(t, inboxItemContaining(t, r, "target", "held a message to you"), "canceled", surface.EvidenceCanceled)
}

func TestAQueuedMessageWhoseQueueRowIsGoneIsNotPending(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	id, err := r.QueueMessageWithKey("target", "row removed", "removed:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`DELETE FROM message_queue WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	assertSettledInInbox(t, inboxItemContaining(t, r, "target", "row removed"), QueueStatusRemoved, surface.EvidenceUnknown)
}
