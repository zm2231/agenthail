package daemon

import (
	"testing"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionJournalKeepsAPromptsMessageIDForSeedAndLive(t *testing.T) {
	for _, mode := range []string{"seed", "live", "seed-metadata"} {
		t.Run(mode, func(t *testing.T) {
			_, reg, fake, from, _ := daemonFixture(t)
			source := &sessionSource{manager: newTestSourceManager(t, reg), session: from, adapter: fake, epoch: "test", appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
			item := surface.TimelineItem{ID: "prompt", Kind: "message", Role: "user", Text: "Run the tests", TurnID: "turn-1", MessageID: "intent-uuid"}
			switch mode {
			case "live":
				source.append(surface.StreamEvent{ID: item.ID, ProviderKey: "timeline:" + item.ID, Operation: "upsert", Kind: item.Kind, Role: item.Role, Text: item.Text, TurnID: item.TurnID, MessageID: item.MessageID})
			case "seed-metadata":
				source.append(surface.StreamEvent{ID: item.ID, ProviderKey: "timeline:" + item.ID, Operation: "upsert", Kind: item.Kind, Role: item.Role, Text: item.Text, TurnID: item.TurnID})
				source.appendSeedItems([]surface.TimelineItem{item})
			default:
				source.appendSeedItems([]surface.TimelineItem{item})
			}
			entry, found, err := reg.SessionJournalEntryByProviderKey(from.ID, "timeline:"+item.ID)
			if err != nil || !found {
				t.Fatalf("journal entry found=%v err=%v", found, err)
			}
			event, err := sessionstream.DecodePayload(entry.Seq, entry.Payload)
			if err != nil || event.MessageID != item.MessageID || event.TurnID != item.TurnID {
				t.Fatalf("stream event=%+v err=%v", event, err)
			}
		})
	}
}
