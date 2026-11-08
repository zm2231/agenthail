package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func storedPersonMessage(t *testing.T, f *humanTurnFixture, sessionID string) surface.PersonMessage {
	t.Helper()
	fingerprint, found, err := f.r.CatalogProjectionFingerprint(sessionID)
	if err != nil || !found {
		t.Fatalf("catalog row of %s: found=%v err=%v", sessionID, found, err)
	}
	var row dashboardSession
	if err := json.Unmarshal([]byte(fingerprint), &row); err != nil {
		t.Fatal(err)
	}
	return row.Capabilities.PersonMessage
}

func TestTheNoticeAPersonSeesMatchesTheRouteTheirSendTakes(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.d.discoverCatalog(context.Background())
	if got := storedPersonMessage(t, f, f.bridged.ID); got != surface.PersonTurn {
		t.Fatalf("personMessage=%q, want a turn while the session has Remote Control", got)
	}

	f.process.Record(f.bridged.ID, f.process.Home, "")
	var atSend surface.PersonMessage
	f.claude.beforeSend = func() { atSend = storedPersonMessage(t, f, f.bridged.ID) }
	f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"after Remote Control ended"}`)

	if len(f.posts) != 0 || len(f.claude.native) != 1 {
		t.Fatalf("posts=%v native=%v, want the send on the socket the process still offers", f.posts, f.claude.native)
	}
	if atSend != surface.PersonNote {
		t.Fatal("the row still promised the person's own turn when the message went out as a note")
	}
}

func TestASendThatFindsRemoteControlGonePublishesTheNoteRouteBeforeDeliveringOnIt(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.d.discoverCatalog(context.Background())
	f.postStatus = http.StatusNotFound
	var atSend surface.PersonMessage
	f.claude.beforeSend = func() { atSend = storedPersonMessage(t, f, f.bridged.ID) }
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"still there?"}`)

	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) || receipt["deliveredAs"] != "note" || len(f.posts) != 1 || len(f.claude.native) != 1 || f.claude.native[0] != "still there?" {
		t.Fatalf("receipt=%v posts=%d native=%v, want one refused post and the message accepted once on the socket", receipt, len(f.posts), f.claude.native)
	}
	if atSend != surface.PersonNote {
		t.Fatal("the row still promised the person's own turn when the message went out on the socket as a note")
	}
	history, err := f.r.ListHistory(10, f.bridged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) < 2 || history[0].Kind != "transport-accepted" || history[0].SendRoute != "peer" || history[1].Kind != "rerouted" || history[1].SendRoute != "human-turn" || history[1].Bridge != "session_bridge" {
		t.Fatalf("history=%+v, want the gone Remote Control attempt recorded with its id, then the socket delivery", history)
	}
	stored, err := f.r.Session(f.bridged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Bridge != "" {
		t.Fatalf("stored bridge=%q, want the gone Remote Control dropped", stored.Bridge)
	}
}
