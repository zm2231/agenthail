package surfaces

import (
	"slices"

	"github.com/zm2231/agenthail/internal/surface"
)

// codexPlaceItems puts a call row an action replaced back on its call's
// record, so a page holds it where the journal's upsert keeps it.
func codexPlaceItems(placed map[int][]surface.TimelineItem, callRecords map[string]int, index int, record map[string]any, items []surface.TimelineItem) []surface.TimelineItem {
	var kept []surface.TimelineItem
	for position, item := range items {
		if at, ok := callRecords[item.ID]; ok && item.ID != "" && at != index && len(placed[at]) > 0 {
			row := slices.Clone(placed[at])
			row[0] = item
			placed[at] = row
			continue
		}
		if item.Kind == "toolCall" && item.ID == "" && position == 0 {
			if key := codexTranscriptItemKey(record, item, 0); key != "" {
				callRecords[key] = index
			}
		}
		kept = append(kept, item)
	}
	return kept
}
