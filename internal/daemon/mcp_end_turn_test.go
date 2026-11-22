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

func TestEndTurnShowsTheAgentTheCutHeadlineAndWhatItNamesWithoutRefusingIt(t *testing.T) {
	f := newAskFixture(t)
	long := "PR #1180 passed review round 3 and the full gate is running before the push goes up to the lead"
	f.turn(t, f.from, "turn-1", func() {
		out, refused := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": long})
		if refused {
			t.Fatalf("a headline with a PR number was refused: %v", out)
		}
		shown := viewRow(t, f.d, f.from.ID).EndTurn.Headline
		then, _ := out["then"].(string)
		for _, want := range []string{`"` + shown + `"`, "names a PR or issue number (#1180)", "names a review round (round 3)", "is 95 characters", "replace it", "write your reply", "no more tools"} {
			if !strings.Contains(then, want) {
				t.Errorf("end_turn result %q does not say %q", then, want)
			}
		}
		out, _ = callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": "The duplicate session fix is ready; waiting on tests"})
		if then, _ := out["then"].(string); then != endTurnThen {
			t.Errorf("a plain headline got %q", then)
		}
	})
	if end := viewRow(t, f.d, f.from.ID).EndTurn; end == nil || end.Headline != "The duplicate session fix is ready; waiting on tests" {
		t.Fatalf("the replacing headline is not the one shown: %+v", end)
	}
}

func TestEndTurnNamesEveryIdItFindsByItsOwnText(t *testing.T) {
	f := newAskFixture(t)
	cases := map[string][]string{
		"PR 12 fixes #13, then pull/14":                          {"names a PR or issue number (PR 12, #13, pull/14)"},
		"Finished fix, #123":                                     {"names a PR or issue number (#123)"},
		"W451 and A12 are done":                                  {"names a work item id (W451, A12)"},
		"r2 passed; the second review round and round 4 are too": {"names a review round (r2, second review round, round 4)"},
		"Landed 73fa66e6a and a0768f1f1":                         {"names a commit hash or id (73fa66e6a, a0768f1f1)"},
	}
	f.turn(t, f.from, "turn-1", func() {
		for headline, wants := range cases {
			out, refused := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": headline})
			if refused {
				t.Fatalf("%q was refused: %v", headline, out)
			}
			then, _ := out["then"].(string)
			for _, want := range wants {
				if !strings.Contains(then, want) {
					t.Errorf("for %q the result %q does not say %q", headline, then, want)
				}
			}
		}
		for _, plain := range []string{"The R2 bucket holds the 3rd party build for the W3C demo", "Q&A and A/B tests on #hashtags passed"} {
			out, _ := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": plain})
			if then, _ := out["then"].(string); then != endTurnThen {
				t.Errorf("for %q the result is %q", plain, then)
			}
		}
	})
}
