package daemon

import (
	"context"
	"encoding/json"
	"testing"
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
