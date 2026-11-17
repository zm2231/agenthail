package registry

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAQueuedMessageKeepsWhenItWasSentAcrossAMove(t *testing.T) {
	r := moveRegistry(t)
	sent := time.Now().Add(-40 * time.Minute).UTC().Truncate(time.Second)
	if _, err := r.QueueMessageWithOptions(moveConversation, "forwarded late", "", surface.SendOptions{SentAt: sent}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions(moveConversation, "queued here", "", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions(moveConversation, "claims the future", "", surface.SendOptions{SentAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), MoveCarry{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions(moveConversation, "after the move", "", surface.SendOptions{SentAt: sent}); err != nil {
		t.Fatal(err)
	}
	held, err := r.HostDeliveries(moveTarget)
	if err != nil || len(held) != 4 {
		t.Fatalf("held=%+v err=%v", held, err)
	}
	sentAt := map[string]time.Time{}
	for _, item := range held {
		var request struct {
			Message string    `json:"message"`
			SentAt  time.Time `json:"sentAt"`
		}
		if err := json.Unmarshal(item.Request, &request); err != nil {
			t.Fatal(err)
		}
		sentAt[request.Message] = request.SentAt
	}
	for _, message := range []string{"forwarded late", "after the move"} {
		if !sentAt[message].Equal(sent) {
			t.Fatalf("%q was held as sent at %s, not %s", message, sentAt[message], sent)
		}
	}
	for _, message := range []string{"queued here", "claims the future"} {
		if at := sentAt[message]; at.Before(sent.Add(30*time.Minute)) || at.After(time.Now().Add(time.Minute)) {
			t.Fatalf("%q was held as sent at %s, not when it was queued", message, at)
		}
	}
}

func TestAClaimedMessageSaysWhenItWasSent(t *testing.T) {
	r := moveRegistry(t)
	sent := time.Now().Add(-25 * time.Minute).UTC().Truncate(time.Second)
	if _, err := r.QueueMessageWithOptions(moveConversation, "from another host", "", surface.SendOptions{SentAt: sent}); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage(moveConversation, time.Now())
	if err != nil || item == nil || !item.QueuedAt.Equal(sent) {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}
