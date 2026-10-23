package registry

import (
	"path/filepath"
	"testing"
	"time"

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
	if err := r.SetAlias("operator", retiredOperatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(retiredOperatorID, "target", ".*"); err != nil {
		t.Fatal(err)
	}
	kept, err := r.AddRoute("child", "target", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version=33`); err != nil {
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
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].ID != kept {
		t.Fatalf("routes=%+v err=%v, want only the route between sessions", routes, err)
	}
	if owner, err := r.LookupAlias("operator"); err == nil {
		t.Fatalf("the operator's alias survived on %q", owner)
	}
	if _, err := r.Session("target"); err != nil || r.QueueCount("target") != 2 {
		t.Fatalf("target: err=%v queue=%d", err, r.QueueCount("target"))
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

func TestAQueuedPersonsMessageKeepsTheNameItArrivedWith(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	if err := r.EnsureSender(surface.PersonSessionID); err != nil {
		t.Fatal(err)
	}
	options := surface.SendOptions{SourceSessionID: surface.PersonSessionID, Author: surface.AuthorPerson, PersonName: "Ada at her Mac"}
	queued, _, err := r.QueueDeliveryWithIntent("target", "queued", "", options)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: surface.PersonSessionID, TargetSessionID: "target", Message: "held", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	held, _, err := r.QueueDeliveryUsingIntent(intent.ID, "held", "", options)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{queued, held} {
		item, err := r.ClaimNextMessage("target", time.Now().Add(time.Hour))
		if err != nil || item == nil || item.ID != id || item.PersonName != "Ada at her Mac" {
			t.Fatalf("claimed %+v err=%v, want queue item %d with the name", item, err, id)
		}
		if err := r.AckMessage(id); err != nil {
			t.Fatal(err)
		}
	}
}
