package ledger

import (
	"fmt"
	"strings"
)

func (b *Board) HeldBy(sessionID string) []WorkItem {
	var held []WorkItem
	for _, item := range b.WorkItems() {
		if item.Assignee == sessionID && item.Held() {
			held = append(held, item)
		}
	}
	return held
}

func (item WorkItem) Held() bool {
	return item.Assignee != "" && (item.State == WorkInProgress || item.State == WorkBlocked || item.State == WorkInReview)
}

func (item WorkItem) LatestLog() *WorkEntry {
	for i := len(item.Timeline) - 1; i >= 0; i-- {
		if item.Timeline[i].Kind == "log" {
			entry := item.Timeline[i]
			return &entry
		}
	}
	return nil
}

func HeldNote(items []WorkItem, mcp bool) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		rejected := ""
		if by, why, ok := item.Rejection(); ok {
			rejected = fmt.Sprintf("%s sent it back from review: %s. Fix that and submit it again. ", by, clipText(why, 200))
		}
		lines = append(lines, fmt.Sprintf("You hold %s (%s). %s%s", item.ID, clipText(item.Text, 120), rejected, HolderGuidance(item.ID, mcp)))
	}
	return strings.Join(lines, "\n")
}
