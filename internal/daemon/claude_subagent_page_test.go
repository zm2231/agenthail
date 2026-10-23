package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeSubagentPageListsWorkingThenRecentAndCountsAll(t *testing.T) {
	now := time.Now()
	var links []surface.ClaudeSubagentLink
	for index := range claudeSubagentsShown + 3 {
		links = append(links, surface.ClaudeSubagentLink{AgentID: string(rune('a' + index)), LastActive: now.Add(time.Duration(index) * time.Minute)})
	}
	links[0].Working = true
	page := newClaudeSubagentPage(links, false)
	if page.total != len(links) || len(page.links) != claudeSubagentsShown+1 {
		t.Fatalf("total=%d shown=%d", page.total, len(page.links))
	}
	if page.links[0].AgentID != "a" || page.links[1].AgentID != string(rune('a'+claudeSubagentsShown+2)) {
		t.Fatalf("order=%v", page.links)
	}
	if links[0].AgentID != "a" || links[1].AgentID != "b" {
		t.Fatal("paging reordered the caller's slice")
	}
	if all := newClaudeSubagentPage(links, true); len(all.links) != len(links) {
		t.Fatalf("all=%d", len(all.links))
	}
	empty, _ := json.Marshal(newClaudeSubagentPage(nil, false).links)
	if string(empty) != "[]" {
		t.Fatalf("no subagents encoded as %s", empty)
	}
}
