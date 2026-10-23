package registry

import (
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSchema34MovesTheOperatorsMessagesToThePersonAndTheCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []surface.Session{
		{ID: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "target"},
		{ID: "child", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Name: "child"},
		{ID: retiredOperatorID, Surface: "agenthail", RuntimeKind: surface.RuntimeOperator, Name: "Agenthail operator"},
	} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.EnsureSender(surface.PersonSessionID); err != nil {
		t.Fatal(err)
	}
	_, personIntent, err := r.QueueDeliveryWithIntent("target", "typed in the app", "", surface.SendOptions{SourceSessionID: retiredOperatorID, Author: surface.AuthorPerson})
	if err != nil {
		t.Fatal(err)
	}
	_, scriptIntent, err := r.QueueDeliveryWithIntent("target", "from a script", "", surface.SendOptions{SourceSessionID: retiredOperatorID})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: retiredOperatorID, TargetSessionID: "target", Message: "sent directly", Status: DeliveryIntentSent, Evidence: surface.EvidenceDelivered})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(retiredOperatorID, "[agenthail delivery failure id=1 target=gone] send failed"); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordSessionOrigin(SessionOrigin{SessionID: "child", Via: OriginAgent, CreatorID: retiredOperatorID}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version=31`); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if r, err = Open(path); err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	for id, want := range map[int64]string{personIntent: surface.PersonSessionID, scriptIntent: surface.CLISessionID, direct.ID: surface.CLISessionID} {
		intent, err := r.DeliveryIntent(id)
		if err != nil || intent.SenderSessionID != want {
			t.Fatalf("intent %d sender=%v err=%v, want %s", id, intent, err, want)
		}
	}
	var personQueued, scriptQueued int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE source_session_id=? AND message='typed in the app'`, surface.PersonSessionID).Scan(&personQueued); err != nil {
		t.Fatal(err)
	}
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE source_session_id=? AND message='from a script'`, surface.CLISessionID).Scan(&scriptQueued); err != nil {
		t.Fatal(err)
	}
	if personQueued != 1 || scriptQueued != 1 || r.QueueCount("target") != 2 {
		t.Fatalf("queued person=%d script=%d target=%d", personQueued, scriptQueued, r.QueueCount("target"))
	}
	if _, err := r.Session(retiredOperatorID); err == nil {
		t.Fatal("the operator row survived")
	}
	if origin, _, err := r.SessionOrigin("child"); err != nil || origin.CreatorID != surface.CLISessionID {
		t.Fatalf("origin=%+v err=%v", origin, err)
	}
	for _, id := range []string{surface.PersonSessionID, surface.CLISessionID} {
		if session, err := r.Session(id); err != nil || session.RuntimeKind != surface.RuntimeOperator {
			t.Fatalf("%s: %+v err=%v", id, session, err)
		}
	}
}
