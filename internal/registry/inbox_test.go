package registry

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func inboxMessages(t *testing.T, r *Registry, sessionID string) map[string]InboxItem {
	t.Helper()
	page, err := r.InboxPage(InboxRequest{SessionID: sessionID, Limit: inboxPageMax})
	if err != nil {
		t.Fatal(err)
	}
	messages := map[string]InboxItem{}
	for _, item := range page.Items {
		messages[item.Message] = item
	}
	return messages
}

func TestEveryQueuedMessageIsInItsTargetsInbox(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	steps := []struct {
		message string
		queue   func(message string) error
	}{
		{"plain", func(m string) error { return r.QueueMessage("target", m) }},
		{"keyed", func(m string) error { _, err := r.QueueMessageWithKey("target", m, "ledger:readback:1"); return err }},
		{"peer", func(m string) error {
			_, err := r.QueueMessageWithOptions("target", m, "peer:target:x:1", surface.SendOptions{BusyDelivery: "queue"})
			return err
		}},
		{"lead notice", func(m string) error {
			_, err := r.QueueMessageWithOptions("target", m, "lead:1", surface.SendOptions{SourceSessionID: "sender", BusyDelivery: "steer"})
			return err
		}},
		{"dispatched", func(m string) error {
			_, _, err := r.QueueDeliveryWithIntent("target", m, "", surface.SendOptions{SourceSessionID: "sender"})
			return err
		}},
		{"person", func(m string) error {
			_, _, err := r.QueueDeliveryWithIntent("target", m, "", surface.SendOptions{Author: surface.AuthorPerson})
			return err
		}},
		{"relayed", func(m string) error {
			_, err := r.QueueRelayMessageWithOptions("target", m, "relay:1:t1", 1, surface.SendOptions{SourceSessionID: "sender"})
			return err
		}},
		{"submitted first", func(m string) error {
			intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: m, Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
			if err != nil {
				return err
			}
			_, _, err = r.QueueDeliveryUsingIntent(intent.ID, m, "", surface.SendOptions{})
			return err
		}},
	}
	for _, step := range steps {
		if err := step.queue(step.message); err != nil {
			t.Fatalf("%s: %v", step.message, err)
		}
	}
	inbox := inboxMessages(t, r, "target")
	for _, step := range steps {
		item, ok := inbox[step.message]
		if !ok || !item.Pending() || item.QueueID == 0 {
			t.Errorf("%s: queued for target but its inbox shows %+v (listed=%v)", step.message, item, ok)
		}
	}
	if got := inbox["peer"].SourceSessionID; got != surface.SystemSessionID {
		t.Errorf("a message with no sender is from %q, want Agenthail", got)
	}
	if got := inbox["lead notice"].SourceSessionID; got != "sender" {
		t.Errorf("a lead notice about sender is from %q", got)
	}
	if got := inbox["person"].SenderSessionID; got != surface.PersonSessionID {
		t.Errorf("the person's message is from %q", got)
	}
}
