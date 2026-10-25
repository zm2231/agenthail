package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func BenchmarkJournalPageAssembly(b *testing.B) {
	for _, tools := range []int{0, 8, 500} {
		b.Run(fmt.Sprintf("tools-%d", tools), func(b *testing.B) {
			r, err := registry.Open(registrytest.Path(b))
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()
			if err := r.RegisterSession(surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
				b.Fatal(err)
			}
			entries := make([]registry.SessionJournalEntry, 0, 2048)
			for turn := 0; len(entries) < 2048; turn++ {
				for index := 0; index < tools+3 && len(entries) < 2048; index++ {
					kind, role, body := "toolCall", "assistant", strings.Repeat("retained transcript ", 32)
					if index == 0 {
						kind, role = "message", "user"
					} else if index == tools+1 {
						kind = "message"
					} else if index == tools+2 {
						kind, role, body = "done", "", ""
					}
					id := fmt.Sprint(len(entries))
					payload := sessionJournalPayload{ItemID: id, Kind: kind, Role: role, Body: body, TurnID: fmt.Sprint(turn), Op: "upsert", TS: "2026-10-25T00:00:00Z"}
					raw, err := json.Marshal(payload)
					if err != nil {
						b.Fatal(err)
					}
					entries = append(entries, registry.SessionJournalEntry{Kind: kind, ProviderKey: id, Payload: raw})
				}
			}
			if _, err := r.ReplaceSessionJournal("s", entries, registry.SessionJournalRetention{Count: 2048, Bytes: 8 << 20}, 1); err != nil {
				b.Fatal(err)
			}
			d := &Daemon{Registry: r}
			for _, limit := range []int{1, 10, 50} {
				b.Run(fmt.Sprintf("limit-%d", limit), func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := d.readJournalPage("s", 0, limit); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
