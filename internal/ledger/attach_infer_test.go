package ledger_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

var boardTail = regexp.MustCompile(`(; \d+ provisional: [^;]*)?(; open questions: .*)?$`)

func withoutBoardTail(readback string) string {
	return boardTail.ReplaceAllString(readback, "")
}

func TestARelationLeftOutIsReadFromTheFlagsAndSaidBack(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "the index leaks a handle"})
	dashboards := goal(t, s, "Dashboards")
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed})
	for i := 2; i <= 16; i++ {
		capture(t, s, project, fmt.Sprintf("claude-prompt:%d", i), "words")
	}
	u := func(i int) string { return fmt.Sprintf("claude-prompt:%d", i) }
	for _, tc := range []struct {
		req  ledger.AttachRequest
		want string
	}{
		{ledger.AttachRequest{UtteranceID: u(2), To: "widget-A1", Clauses: []string{"rank by recency"}},
			"+1 clause on widget-A1 (widget-C3) (no --as, read as extends: --to is an ask)"},
		{ledger.AttachRequest{UtteranceID: u(3), To: "widget-C1", Clauses: []string{"with typo tolerance"}},
			"+1 clause on widget-A1 (widget-C4) (no --as, read as extends: --to is a clause, and new words add to it; --as refines rewords it; --to widget-C1 read as widget-A1)"},
		{ledger.AttachRequest{UtteranceID: u(4), To: "widget-W1", Clauses: []string{"index titles too"}},
			"+1 clause on widget-A1 (widget-C5) (no --as, read as extends: --to widget-W1 serves a clause, and new words add to it; --as refines rewords it; --to widget-W1 read as widget-C1; --to widget-C1 read as widget-A1)"},
		{ledger.AttachRequest{UtteranceID: u(5)},
			"claude-prompt:5 is conversation (no --as, read as conversation: no --to and no --clause)"},
		{ledger.AttachRequest{UtteranceID: u(6), Clauses: []string{"build an export"}},
			"+widget-A2 on widget-G3 (widget) with 1 clause (widget-C6) (no --as, read as new-ask: no --to; no --to, read as widget-G3 (widget), a goal made for the project)"},
		{ledger.AttachRequest{UtteranceID: u(7), To: ledger.ProjectTarget, Clauses: []string{"and a report"}},
			"+widget-A3 on widget-G3 (widget) with 1 clause (widget-C7) (no --as, read as new-ask: --to is the project; --to project read as widget-G3 (widget))"},
		{ledger.AttachRequest{UtteranceID: u(8), To: dashboards, Clauses: []string{"a dashboard"}},
			"+widget-A4 on widget-G2 (Dashboards) with 1 clause (widget-C8) (no --as, read as new-ask: --to is a goal)"},
		{ledger.AttachRequest{UtteranceID: u(9), To: "widget-W2", Clauses: []string{"close the handle"}},
			"+widget-A5 on widget-G3 (widget) with 1 clause (widget-C9) (no --as, read as new-ask: --to is a found work item; found work widget-W2 hangs off no clause, so --to widget-W2 read as the project; --to project read as widget-G3 (widget))"},
		{ledger.AttachRequest{UtteranceID: u(10), To: "widget-G1"},
			"claude-prompt:10 is a reaction to widget-G1 (Search); nothing changed (no --as, read as reacts: no --clause)"},
		{ledger.AttachRequest{UtteranceID: u(11), To: "widget-C2", Adopt: true},
			"widget-C2 adopted (no --as, read as reacts: --adopt or --reject was given)"},
		{ledger.AttachRequest{UtteranceID: u(12), To: "widget-C3", Reject: true},
			"widget-C3 cut, pending the person's ack (widget-X1) (no --as, read as reacts: --adopt or --reject was given)"},
		{ledger.AttachRequest{UtteranceID: u(13), To: "widget-X1", Ack: true},
			"widget-X1 acked: widget-C3 is cut (no --as, read as reacts: --ack was given)"},
		{ledger.AttachRequest{UtteranceID: u(14), To: "widget-A1", Delegate: "helper"},
			"widget-A1 delegated to helper (no --as, read as delegates: --session names a delegate)"},
	} {
		result := attach(t, s, tc.req)
		if got, want := withoutBoardTail(result.Readback), "intent ledger: "+tc.want; got != want {
			t.Errorf("attach %+v\n got  %q\n want %q", tc.req, got, want)
		}
	}
	if c := clause(t, board(t, s), "widget-C1"); c.Text != "build search" {
		t.Fatalf("an attach without --as reworded a clause: %+v", c)
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: u(15), To: "widget-C1", Reject: true})
	refused(t, s, ledger.AttachRequest{UtteranceID: u(16), To: "widget-X2"}, "widget-X2 is a cut; acking it drops what it cuts for good, so give --ack to ack it")
	refused(t, s, ledger.AttachRequest{UtteranceID: u(16), To: "widget-Z9", Clauses: []string{"x"}}, `"widget-Z9" is not a goal, ask, clause or work item`)
}

func TestARelationLeftOutOnAQuestionAnswersItWithoutRewritingAnything(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	for i := 2; i <= 5; i++ {
		capture(t, s, project, fmt.Sprintf("claude-prompt:%d", i), "full-text")
	}
	onClause := askQuestion(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which kind of search?", DefaultAnswer: "full-text"})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", To: onClause.ID, Clauses: []string{"build full-text search"}},
		"an answer to widget-Q1 with a --clause rewords widget-C1, so give --as answers to apply it")
	if c := clause(t, board(t, s), "widget-C1"); c.Text != "build search" {
		t.Fatalf("a refused answer reworded the clause: %+v", c)
	}
	onProject := askQuestion(t, s, ledger.QuestionRequest{To: ledger.ProjectTarget, Text: "which database?", DefaultAnswer: "sqlite"})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", To: onProject.ID, Clauses: []string{"use sqlite"}},
		"an answer to widget-Q2 with a --clause records a decision on the project, so give --as answers to apply it")
	onAsk := askQuestion(t, s, ledger.QuestionRequest{To: "widget-A1", Text: "anything else?", DefaultAnswer: "no"})
	added := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:4", To: onAsk.ID, Clauses: []string{"rank by recency"}})
	if got := withoutBoardTail(added.Readback); got != "intent ledger: widget-Q3 answered (+widget-C2 on widget-A1) (no --as, read as answers: --to is a question)" {
		t.Fatalf("readback = %q", got)
	}
	plain := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:5", To: onClause.ID})
	if got := withoutBoardTail(plain.Readback); got != "intent ledger: widget-Q1 answered (no --as, read as answers: --to is a question)" {
		t.Fatalf("readback = %q", got)
	}
	if c := clause(t, board(t, s), "widget-C1"); c.Text != "build search" {
		t.Fatalf("an answer without --clause reworded the clause: %+v", c)
	}
}
