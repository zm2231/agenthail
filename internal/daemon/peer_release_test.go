package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAReleasedMessageIsNeverSentAgain(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		name := "queued"
		if claimed {
			name = "claimed"
		}
		t.Run(name, func(t *testing.T) {
			d, r, fake, sender, target := daemonFixture(t)
			queueID, intentID, err := r.QueueDeliveryWithIntent(target.ID, "parked by Claude", "", surface.SendOptions{SourceSessionID: sender.ID})
			if err != nil {
				t.Fatal(err)
			}
			intent, err := r.DeliveryIntent(intentID)
			if err != nil {
				t.Fatal(err)
			}
			peer := registry.PeerMessage{MsgID: intent.UUID, TargetSessionID: target.ID}
			if changed, err := r.HoldPeerMessage(peer, "waiting for approval"); err != nil || !changed {
				t.Fatalf("hold changed=%v err=%v", changed, err)
			}
			if r.QueueCount(sender.ID) == 0 {
				t.Fatal("the sender was not told the message is held")
			}
			if claimed {
				item, err := r.ClaimNextMessage(target.ID, time.Now())
				if err != nil || item == nil || item.ID != queueID {
					t.Fatalf("claim=%+v err=%v", item, err)
				}
			}
			if changed, err := r.ReleasePeerMessage(peer); err != nil || !changed {
				t.Fatalf("release changed=%v err=%v", changed, err)
			}
			if claimed {
				if err := r.AckMessageWithEvidence(queueID, target.ID, 0, surface.EvidenceTransportAccepted, ""); err == nil {
					t.Fatal("a late ack settled the released message a second time")
				}
			}
			session, err := r.Session(target.ID)
			if err != nil {
				t.Fatal(err)
			}
			d.drainMessageQueue(context.Background(), fake, session)
			if len(fake.sent) != 0 {
				t.Fatalf("sent=%v, want the released message left with Claude", fake.sent)
			}
			if pending := r.QueueCount(target.ID); pending != 0 {
				t.Fatalf("target queue=%d, want the released message settled", pending)
			}
			if pending := r.QueueCount(sender.ID); pending != 0 {
				t.Fatalf("sender queue=%d, want the stale held notice withdrawn", pending)
			}
			released, err := r.DeliveryIntent(intentID)
			if err != nil {
				t.Fatal(err)
			}
			if released.Status != registry.DeliveryIntentDelivered {
				t.Fatalf("intent=%+v, want delivered", released)
			}
			if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
				t.Fatalf("problems=%+v err=%v", problems, err)
			}
		})
	}
}
