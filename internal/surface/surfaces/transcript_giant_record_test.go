package surfaces

import (
	"math/rand"
	"strings"
	"testing"
)

func TestAPageCutsAGiantToolResultButKeepsTheReplyWhole(t *testing.T) {
	reply := strings.Repeat("a", 3<<20/2)
	records := func(int, *rand.Rand) []map[string]any {
		at := "2026-11-06T09:00:00Z"
		return []map[string]any{
			{"type": "user", "uuid": "prompt-1", "timestamp": at, "message": map[string]any{"role": "user", "content": "read the log"}},
			{"type": "assistant", "uuid": "call-1", "timestamp": at, "message": map[string]any{"id": "message-1", "content": []map[string]any{{"type": "tool_use", "id": "tool-1", "name": "Bash", "input": map[string]any{"command": "cat log"}}}}},
			{"type": "user", "uuid": "result-1", "timestamp": at, "toolUseResult": map[string]any{"stdout": strings.Repeat("y", 3<<20)}, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "tool-1", "content": strings.Repeat("x", 3<<20)}}}},
			{"type": "assistant", "uuid": "answer-1", "timestamp": at, "message": map[string]any{"id": "message-1", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": reply}}}},
		}
	}
	transcript := writePageTranscript(t, records, 1, 1)
	page := readPageWithFirstWindow(t, 64<<10, transcript.path, "claude", 0, 10)
	samePage(t, "giant records", page, readPageWithFirstWindow(t, timelineReadBudget, transcript.path, "claude", 0, 10))
	if len(page.Exchanges) != 1 || page.Exchanges[0].Assistant != reply || page.Exchanges[0].User != "read the log" {
		t.Fatalf("exchanges = %d; the reply must come back whole", len(page.Exchanges))
	}
	cut := false
	for _, item := range page.Items {
		if item.TurnID != "prompt-1" {
			t.Fatalf("item %s %q is in turn %q, want prompt-1", item.Kind, item.ID, item.TurnID)
		}
		if item.Kind == "toolResult" {
			cut = item.Truncated && len(item.Text) < 3<<20
		}
	}
	if !cut {
		t.Fatal("the 3 MB tool result was not shown cut and marked truncated")
	}
}
