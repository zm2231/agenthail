package daemon

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Claude Opus 5.5 returns text it writes between tool calls as a summarized
// thinking block, so a reply is kept only when it follows the turn's last
// tool call. end_turn's result is what the model reads next, and it has to ask
// for that reply.
func TestEndTurnTellsTheModelToWriteItsReplyNextWithNoMoreToolCalls(t *testing.T) {
	f := newAskFixture(t)
	f.turn(t, f.from, "turn-1", func() {
		result, err := f.person.CallTool(t.Context(), callParams("agenthail_end_turn", map[string]any{"headline": "Fixed the parser"}))
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("end_turn refused: %+v", result.Content)
		}
		var read strings.Builder
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				read.WriteString(text.Text)
			}
		}
		for _, want := range []string{"write your reply", "no more tools"} {
			if !strings.Contains(read.String(), want) {
				t.Errorf("end_turn result %q does not say %q", read.String(), want)
			}
		}
	})
}
