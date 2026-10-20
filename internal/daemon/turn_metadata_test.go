package daemon

import (
	"reflect"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAJournalPageCarriesTheMetadataOfEachTurn(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "turns", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	duration := int64(4200)
	live := surface.NewTurnMetadata(surface.TurnCompleted, surface.TurnMeasure{Model: "gpt-fixture", DurationMs: &duration, Usage: &surface.TurnUsage{InputTokens: 250, OutputTokens: 50}})
	seeded := surface.NewTurnMetadata(surface.TurnInterrupted, surface.TurnMeasure{Model: "gpt-fixture"})
	for _, turn := range []struct {
		id, status string
		meta       *surface.TurnMetadata
	}{{"turn-1", "", live}, {"turn-2", "turn_aborted", nil}} {
		source.append(surface.StreamEvent{ID: turn.id + ":prompt", ProviderKey: turn.id + ":prompt", Version: 1, Operation: "upsert", Final: true, Kind: "message", Role: "user", Text: "prompt " + turn.id, TurnID: turn.id})
		source.append(surface.StreamEvent{ID: turn.id + ":answer", ProviderKey: turn.id + ":answer", Version: 1, Operation: "upsert", Final: true, Kind: "message", Role: "assistant", Text: "answer " + turn.id, TurnID: turn.id})
		source.append(surface.StreamEvent{ID: turn.id + ":done", ProviderKey: "timeline:" + turn.id + ":done", Operation: "phase", Kind: "done", Status: turn.status, TurnID: turn.id, Turn: turn.meta})
	}
	source.appendSeedItems([]surface.TimelineItem{{ID: "turn-2:done", Kind: "done", Status: "turn_aborted", TurnID: "turn-2", Turn: seeded}})
	history := surface.NewTurnMetadata(surface.TurnCompleted, surface.TurnMeasure{Model: "gpt-history"})
	source.appendSeedItems([]surface.TimelineItem{
		{ID: "turn-3:prompt", Kind: "message", Role: "user", Text: "prompt turn-3", TurnID: "turn-3"},
		{ID: "turn-3:answer", Kind: "message", Role: "assistant", Text: "answer turn-3", TurnID: "turn-3"},
		{ID: "turn-3:done", Kind: "done", TurnID: "turn-3", Turn: history},
	})

	page, err := d.readJournalPage(from.ID, 0, 5)
	if err != nil || len(page.Exchanges) != 3 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for index, want := range []*surface.TurnMetadata{live, seeded, history} {
		if !reflect.DeepEqual(page.Exchanges[index].Turn, want) {
			t.Fatalf("exchange %d turn=%+v want %+v", index, page.Exchanges[index].Turn, want)
		}
	}
}
