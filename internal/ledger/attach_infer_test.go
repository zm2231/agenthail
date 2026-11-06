package ledger_test

import (
	"regexp"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestARelationLeftOutIsReadFromTheTargetAndSaidBack(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index")
	for ref, text := range map[string]string{"claude-prompt:2": "rank by recency", "claude-prompt:3": "full-text search", "claude-prompt:4": "thanks",
		"claude-prompt:5": "and an export", "claude-prompt:6": "nice", "claude-prompt:7": "the index, ranked", "claude-prompt:8": "drop ranking", "claude-prompt:9": "ok, drop it"} {
		capture(t, s, project, ref, text)
	}
	for _, tc := range []struct {
		req  ledger.AttachRequest
		want string
	}{
		{ledger.AttachRequest{UtteranceID: "claude-prompt:2", To: "widget-A1", Clauses: []string{"rank by recency"}},
			"+1 clause on widget-A1 (widget-C2) (no --as, read as extends: --to is an ask)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:3", To: "widget-C1", Clauses: []string{"build full-text search"}},
			"widget-C1 refined (no --as, read as refines: --to is a clause)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:4"},
			"claude-prompt:4 is conversation (no --as, read as conversation: no --to and no --clause)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:5", Clauses: []string{"build an export"}},
			"+widget-A2 on widget-G2 (widget) with 1 clause (widget-C3) (no --as, read as new-ask: no --to; no --to, read as widget-G2 (widget), a goal made for the project)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:6", To: "widget-G1"},
			"claude-prompt:6 is a reaction to widget-G1 (Search); nothing changed (no --as, read as reacts: no --clause)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:7", To: "widget-W1", Clauses: []string{"build full-text search, ranked"}},
			"widget-C1 refined (no --as, read as refines: --to widget-W1 serves a clause; --to widget-W1 read as widget-C1)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:8", To: "widget-C2", Reject: true},
			"widget-C2 cut, pending the person's ack (widget-X1) (no --as, read as reacts: --adopt or --reject was given)"},
		{ledger.AttachRequest{UtteranceID: "claude-prompt:9", To: "widget-X1"},
			"widget-X1 acked: widget-C2 is cut (no --as, read as reacts: --to is a cut)"},
	} {
		result := attach(t, s, tc.req)
		if got, want := trimUnattached(result.Readback), "intent ledger: "+tc.want; got != want {
			t.Errorf("attach %+v\n got  %q\n want %q", tc.req, got, want)
		}
	}
}

var boardTail = regexp.MustCompile(`(; \d+ unattached: [^;]*)?(; open questions: .*)?$`)

func trimUnattached(readback string) string {
	return boardTail.ReplaceAllString(readback, "")
}

func TestARelationLeftOutOnAQuestionAnswersIt(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "full-text")
	q := askQuestion(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which kind of search?", DefaultAnswer: "full-text"})
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", To: q.ID})
	if result.Readback != "intent ledger: widget-Q1 answered (no --as, read as answers: --to is a question)" {
		t.Fatalf("readback = %q", result.Readback)
	}
}
