package daemon

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestJournalPageMarksAgentMessages(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newTestSourceManager(t, reg), session: from, adapter: fake, epoch: "agents", appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
	source.append(surface.StreamEvent{ID: "prompt", ProviderKey: "timeline:prompt", Operation: "upsert", Final: true, Kind: "message", Role: "user", Text: "[from reviewer via #ops] Please rebase"})
	source.append(surface.StreamEvent{ID: "send", ProviderKey: "timeline:send", Operation: "upsert", Final: true, Kind: "toolCall", Title: "Bash", Text: `{"command":"agenthail send @collab \"done\""}`})
	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	agents := map[string]*surface.AgentMessage{}
	for _, item := range page.Items {
		agents[item.ID] = item.Agent
		if item.ID == "prompt" && (item.Role != "peer" || item.Sender != "reviewer" || item.Text != "Please rebase") {
			t.Fatalf("prompt item = %+v", item)
		}
	}
	if got := agents["prompt"]; got == nil || *got != (surface.AgentMessage{Direction: surface.AgentIn, Kind: surface.AgentKindPeer, Peer: "reviewer", Channel: "ops", Summary: "Please rebase"}) {
		t.Fatalf("prompt agent = %+v", got)
	}
	if got := agents["send"]; got == nil || *got != (surface.AgentMessage{Direction: surface.AgentOut, Kind: surface.AgentKindSend, Peer: "@collab", Summary: "done", Body: "done"}) {
		t.Fatalf("send agent = %+v", got)
	}
	if len(page.Exchanges) == 0 || page.Exchanges[0].Origin != "peer" || page.Exchanges[0].Sender != "reviewer" {
		t.Fatalf("exchanges = %+v", page.Exchanges)
	}
}
