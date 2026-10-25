package surfaces

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudePeerMessageFromThePersonIsTheirMessage(t *testing.T) {
	words := "Zain" + surface.PersonPeerPreamble + "Ship the fix"
	envelope := `<cross-session-message from="uds:/tmp/s.sock" from-session="` + personPeerUUID + `" from-name="agenthail/person: Zain">` + "\n" + words + "\n</cross-session-message>"
	agent := `<cross-session-message from="uds:/tmp/s.sock" from-session="11111111-2222-3333-4444-555555555555" from-name="reviewer">Please rebase</cross-session-message>`
	line := func(uuid, name, body, content string) string {
		record, _ := json.Marshal(map[string]any{"type": "user", "uuid": uuid, "origin": map[string]any{"kind": "peer", "name": name, "msg_id": uuid, "body": body}, "message": map[string]any{"content": content}})
		return string(record) + "\n"
	}
	path := timelineFixture(t, line("p1", "agenthail/person: Zain", words, envelope)+line("p2", "reviewer", "Please rebase", agent))
	page, err := readTimeline(context.Background(), path, "claude", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var person, peer *surface.TimelineItem
	for index := range page.Items {
		switch page.Items[index].ID {
		case "peer:p1":
			person = &page.Items[index]
		case "peer:p2":
			peer = &page.Items[index]
		}
	}
	if person == nil || person.Role != "user" || person.Origin != surface.OriginPerson || person.Text != "Ship the fix" {
		t.Fatalf("person item = %+v", person)
	}
	if peer == nil || peer.Role != "peer" || peer.Sender != "reviewer" || peer.Text != "Please rebase" {
		t.Fatalf("peer item = %+v", peer)
	}
}
