package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func storedTurnNotice(t *testing.T, f *humanTurnFixture, sessionID string) string {
	t.Helper()
	fingerprint, found, err := f.r.CatalogProjectionFingerprint(sessionID)
	if err != nil || !found {
		t.Fatalf("catalog row of %s: found=%v err=%v", sessionID, found, err)
	}
	var row dashboardSession
	if err := json.Unmarshal([]byte(fingerprint), &row); err != nil {
		t.Fatal(err)
	}
	return row.Capabilities.TurnNotice
}

func TestTheNoticeAPersonSeesMatchesTheRouteTheirSendTakes(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.d.discoverCatalog(context.Background())
	if notice := storedTurnNotice(t, f, f.bridged.ID); notice != "" {
		t.Fatalf("notice=%q, want none while the session has Remote Control", notice)
	}

	f.process.Record(f.bridged.ID, f.process.Home, "")
	noticeAtSend := ""
	f.claude.beforeSend = func() { noticeAtSend = storedTurnNotice(t, f, f.bridged.ID) }
	f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"after Remote Control ended"}`)

	if len(f.posts) != 0 || len(f.claude.native) != 1 {
		t.Fatalf("posts=%v native=%v, want the send on the socket the process still offers", f.posts, f.claude.native)
	}
	if noticeAtSend == "" {
		t.Fatal("the row still promised the person's own turn when the message went out as a note")
	}
}

func TestASendThatFindsRemoteControlGoneIsDeliveredAndRecordsTheRouteItTook(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.postStatus = http.StatusNotFound
	peers := claudetest.ListenPeerManager(t, f.home)
	receipt := f.send(t, `{"action":"send","author":"person","sessionId":"conversation-bridge","message":"still there?"}`)

	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) || peers.Count("still there?") != 1 || len(f.posts) != 1 {
		t.Fatalf("receipt=%v peer messages=%v posts=%d, want one refused post and the message accepted on the socket", receipt, peers.Messages(), len(f.posts))
	}
	history, err := f.r.ListHistory(10, f.bridged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 || history[0].Kind != "transport-accepted" || history[0].SendRoute != "peer" || history[0].Bridge != "session_bridge" {
		t.Fatalf("history=%+v, want the send recorded on the peer route with the Remote Control id it was routed by", history)
	}
	stored, err := f.r.Session(f.bridged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Bridge != "" {
		t.Fatalf("stored bridge=%q, want the gone Remote Control dropped", stored.Bridge)
	}
}
