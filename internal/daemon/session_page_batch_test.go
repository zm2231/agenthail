package daemon

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestJournalPagesKeepWholeTurnsAcrossGrowingBatches(t *testing.T) {
	for _, tools := range []int{0, 8, 260} {
		t.Run(fmt.Sprintf("tools-%d", tools), func(t *testing.T) {
			d, r, _, session, _ := daemonFixture(t)
			var entries []registry.SessionJournalEntry
			for turn := 0; turn < 6; turn++ {
				id := fmt.Sprint(turn)
				rows := []sessionJournalPayload{
					{ItemID: id + "-instructions", Kind: "instructions", TurnID: id, Body: "instructions"},
					{ItemID: id + "-prompt", Kind: "message", Role: "user", TurnID: id, Body: "question " + id},
				}
				for tool := 0; tool < tools; tool++ {
					rows = append(rows, sessionJournalPayload{ItemID: fmt.Sprintf("%s-tool-%d", id, tool), Kind: "toolCall", TurnID: id, Body: "tool input"})
				}
				rows = append(rows,
					sessionJournalPayload{ItemID: id + "-state", Kind: "context"},
					sessionJournalPayload{ItemID: id + "-reply", Kind: "message", Role: "assistant", TurnID: id, Body: "answer " + id},
					sessionJournalPayload{ItemID: id + "-done", Kind: "done", TurnID: id},
				)
				for _, row := range rows {
					raw, err := json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
					entries = append(entries, registry.SessionJournalEntry{Kind: row.Kind, ProviderKey: row.ItemID, Payload: raw})
				}
			}
			retention := registry.SessionJournalRetention{Count: 2048, Bytes: 8 << 20}
			if _, err := r.ReplaceSessionJournal(session.ID, entries, retention, 1); err != nil {
				t.Fatal(err)
			}
			changed := sessionJournalPayload{ItemID: "0-reply", Kind: "message", Role: "assistant", TurnID: "0", Body: "updated answer"}
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: session.ID, Kind: "message", ProviderKey: changed.ItemID, Payload: raw}, retention); err != nil {
				t.Fatal(err)
			}
			var cursor uint64
			for turn := 5; turn >= 0; turn-- {
				page, err := d.readJournalPage(session.ID, cursor, 1)
				if err != nil {
					t.Fatal(err)
				}
				id := fmt.Sprint(turn)
				answer := "answer " + id
				if turn == 0 {
					answer = "updated answer"
				}
				if len(page.Exchanges) != 1 || page.Exchanges[0].User != "question "+id || page.Exchanges[0].Assistant != answer {
					t.Fatalf("turn %d: %+v", turn, page.Exchanges)
				}
				if page.Truncated || len(page.Items) != tools+4 || page.Items[0].ID != id+"-instructions" || page.Items[len(page.Items)-1].ID != id+"-done" {
					t.Fatalf("turn %d: truncated=%v items=%+v", turn, page.Truncated, page.Items)
				}
				if turn > 0 && (page.NextBefore <= 0 || cursor > 0 && uint64(page.NextBefore) >= cursor) {
					t.Fatalf("cursor %d -> %d", cursor, page.NextBefore)
				}
				cursor = uint64(page.NextBefore)
			}
		})
	}
}
