package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func BenchmarkAPIJSONSessionPage(b *testing.B) {
	items := make([]surface.TimelineItem, 0, 200)
	for index := range 200 {
		items = append(items, surface.TimelineItem{ID: fmt.Sprint(index), Kind: "text", Role: "assistant", Title: "text", Text: strings.Repeat("a reply line with \"quotes\" and é ", 16), Timestamp: "2026-11-06T01:02:03.000000000Z", TurnID: fmt.Sprint(index / 4)})
	}
	page := map[string]any{"exchanges": surface.ExchangesFromItems(items, "journal"), "timeline": &surface.SessionTimeline{Items: items, Source: "journal"}}
	encoded, err := json.Marshal(page)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(encoded)))
	b.Run("plain", func(b *testing.B) {
		for b.Loop() {
			if _, err := json.Marshal(page); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("api", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := marshalAPIJSON(page); err != nil {
				b.Fatal(err)
			}
		}
	})
}
