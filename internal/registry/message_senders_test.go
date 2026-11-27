package registry

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAQueuedMessageIsSentUnderAnIdThatNamesItsSender(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	delivery, _, err := r.QueueDeliveryWithIntent("target", "Tag the release", "", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	notice, err := r.QueueMessageWithKey("target", "[agenthail] The build is green", "notice")
	if err != nil {
		t.Fatal(err)
	}
	for queueID, want := range map[int64]string{delivery: "sender", notice: surface.SystemSessionID} {
		id, err := r.DeliveryIntentUUIDForQueue(queueID)
		if err != nil || id == "" {
			t.Fatalf("queued message id %q: %v", id, err)
		}
		if sender, found, err := r.MessageSender(id); err != nil || !found || sender != want {
			t.Fatalf("queue item %d: sender=%q found=%v err=%v, want %q", queueID, sender, found, err, want)
		}
	}
}

func TestAFirstTurnAgenthailComposedStaysAgenthailsWhenItsDeliveryIsRecordedLater(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	const id = "6f1c1d2e-8a8b-4c5d-9e0f-112233445566"
	if err := r.RecordMessageSender(id, surface.SystemSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordSessionCreationIntent("target", surface.PersonSessionID, id, "start the lead"); err != nil {
		t.Fatal(err)
	}
	if sender, found, err := r.MessageSender(id); err != nil || !found || sender != surface.SystemSessionID {
		t.Fatalf("sender=%q found=%v err=%v", sender, found, err)
	}
}

func TestUpgradingRecordsTheSenderOfEveryEarlierDeliveryFromThen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "sender", "target")
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: "Rebase onto main", Status: DeliveryIntentSent, Evidence: surface.EvidenceDelivered})
	if err != nil {
		t.Fatal(err)
	}
	created, err := r.MessageSendersSince("target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`DELETE FROM message_senders; DELETE FROM schema_migrations WHERE name=?`, messageSendersMigration); err != nil {
		t.Fatal(err)
	}
	r.Close()

	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if sender, found, err := r.MessageSender(intent.UUID); err != nil || !found || sender != "sender" {
		t.Fatalf("an earlier delivery's sender=%q found=%v err=%v", sender, found, err)
	}
	if upgraded, err := r.MessageSendersSince("target"); err != nil || !upgraded.After(created) {
		t.Fatalf("recording began %v, created %v: %v", upgraded, created, err)
	}
}

func TestAMoveTellsTheNextHostUntilWhenItCannotKnowWhoSentAMessage(t *testing.T) {
	r := openTestRegistry(t)
	recording, err := r.MessageSendersSince(moveConversation)
	if err != nil {
		t.Fatal(err)
	}
	ahead := recording.Add(3 * time.Hour)
	if _, err := r.PrepareMoveIn("move-1", moveConversation, "h00000000000a", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: moveConversation, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
	if _, err := r.TakeMovedSession("move-1", session, "", time.Time{}, nil, MoveCarry{SendersUnknownBefore: ahead.UnixMilli()}, "", MovedCoordinatorPlace{}); err != nil {
		t.Fatal(err)
	}
	if since, err := r.MessageSendersSince(moveConversation); err != nil || !since.Equal(time.UnixMilli(ahead.UnixMilli())) {
		t.Fatalf("senders known here since %v, want the source's %v: %v", since, ahead, err)
	}
	r.now = func() time.Time { return ahead.Add(-time.Hour) }
	carry, err := r.MoveCarry(moveConversation, "")
	if err != nil || carry.SendersUnknownBefore != ahead.UnixMilli() {
		t.Fatalf("moving it on carries %d, want %d: %v", carry.SendersUnknownBefore, ahead.UnixMilli(), err)
	}
}
