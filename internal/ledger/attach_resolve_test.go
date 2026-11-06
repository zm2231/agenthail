package ledger_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func readback(t *testing.T, result ledger.AttachResult, want string) {
	t.Helper()
	if result.Readback != "intent ledger: "+want {
		t.Fatalf("readback = %q\nwant      %q", result.Readback, "intent ledger: "+want)
	}
}

func TestWordsForTheProjectGetAGoalNamedAfterIt(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "build search")
	capture(t, s, project, "claude-prompt:2", "and an export")
	first := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: ledger.ProjectTarget, Clauses: []string{"build search"}})
	readback(t, first, "+widget-A1 on widget-G1 (widget) with 1 clause (widget-C1) (--to project read as widget-G1 (widget), a goal made for the project); 1 unattached: claude-prompt:2")
	second := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsNewAsk, Clauses: []string{"build an export"}})
	readback(t, second, "+widget-A2 on widget-G1 (widget) with 1 clause (widget-C2) (no --to, read as widget-G1 (widget))")
	if goals := board(t, s).Goals(); len(goals) != 1 || goals[0].Name != "widget" {
		t.Fatalf("goals = %+v", goals)
	}
}

func TestExtendingAGoalPicksItsOnlyAskOrStartsOne(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "rank by recency")
	capture(t, s, project, "claude-prompt:3", "also a dashboard")
	capture(t, s, project, "claude-prompt:4", "and alerts")
	one := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-G1", Clauses: []string{"rank by recency"}})
	readback(t, one, "+1 clause on widget-A1 (widget-C2) (--to widget-G1 read as widget-A1); 2 unattached: claude-prompt:3, claude-prompt:4")
	dashboards := goal(t, s, "Dashboards")
	none := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsExtends, To: dashboards, Clauses: []string{"a dashboard"}})
	readback(t, none, "+widget-A2 on widget-G2 (Dashboards) with 1 clause (widget-C3) (widget-G2 (Dashboards) has no ask, so --as extends became a new ask on it); 1 unattached: claude-prompt:4")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:4", As: ledger.AsNewAsk, To: dashboards, Clauses: []string{"alerts"}})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:4", As: ledger.AsDelegates, To: dashboards}, "widget-G2 (Dashboards) has 2 asks; name the one to delegate: widget-A2, widget-A3")
	several := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:4", As: ledger.AsExtends, To: dashboards, Clauses: []string{"page on alerts"}})
	readback(t, several, "+widget-A4 on widget-G2 (Dashboards) with 1 clause (widget-C5) (widget-G2 (Dashboards) has 2 asks, so --as extends became a new ask on it)")
}

func TestAWorkItemOrClauseStandsForWhatItHangsOff(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "the index leaks a handle"})
	capture(t, s, project, "claude-prompt:2", "use sqlite fts")
	capture(t, s, project, "claude-prompt:3", "never block the ui")
	capture(t, s, project, "claude-prompt:4", "we keep logs a week")
	asked := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsDecides, To: "widget-W1", Clauses: []string{"use sqlite fts"}})
	readback(t, asked, "+widget-D1 decided on widget-G1 (Search) (--to widget-W1 read as widget-C1; --to widget-C1 read as widget-G1 (Search)); 2 unattached: claude-prompt:3, claude-prompt:4")
	constraint := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsConstrains, To: "widget-C1", Clauses: []string{"never block the ui"}})
	readback(t, constraint, "+1 constraint on widget-A1 (widget-C2) (--to widget-C1 read as widget-A1); 1 unattached: claude-prompt:4")
	found := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:4", As: ledger.AsDecides, To: "widget-W2", Clauses: []string{"keep logs a week"}})
	readback(t, found, "+widget-D2 decided on the project (found work widget-W2 hangs off no clause, so --to widget-W2 read as the project)")
}

func TestARefinementOfAnAskWithOneClauseRefinesThatClause(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "full-text search, I mean")
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsRefines, To: "widget-A1", Clauses: []string{"build full-text search"}})
	readback(t, result, "widget-C1 refined (--to widget-A1 read as widget-C1)")
	if c := clause(t, board(t, s), "widget-C1"); c.Text != "build full-text search" {
		t.Fatalf("refined = %+v", c)
	}
	capture(t, s, project, "claude-prompt:3", "and exports")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"exports"}})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsRefines, To: "widget-A1", Clauses: []string{"x"}}, "--as refines takes a clause and widget-A1 has 2; name one: widget-C1, widget-C2")
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsExtends, To: "widget-Z9", Clauses: []string{"x"}}, `"widget-Z9" is not a goal, ask, clause or work item`)
}

func TestConversationAndPlainReactionsAreRecordedAndSayWhatWasLeftOut(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "thanks, nice")
	capture(t, s, project, "claude-prompt:3", "love that")
	capture(t, s, project, "claude-prompt:4", "yes, cache it, and per user")
	talk := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsConversation, To: "widget-G1", Clauses: []string{"nice"}})
	readback(t, talk, "claude-prompt:2 is conversation; --to widget-G1 and the --clause not recorded: conversation binds nothing (attach --as constrains to bind it); 2 unattached: claude-prompt:3, claude-prompt:4")
	plain := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsReacts, To: "widget-C1"})
	readback(t, plain, "claude-prompt:3 is a reaction to widget-C1; nothing changed (add --adopt or --reject to change it); 1 unattached: claude-prompt:4")
	if c := clause(t, board(t, s), "widget-C1"); c.State != ledger.StateCaptured || len(c.Cuts) != 0 {
		t.Fatalf("a plain reaction changed the clause: %+v", c)
	}
	if to := attachedTo(t, s, "claude-prompt:2"); to != "" {
		t.Fatalf("conversation recorded --to %q it said it did not keep", to)
	}
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed})
	adopted := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:4", As: ledger.AsReacts, To: "widget-C2", Adopt: true, Clauses: []string{"cache results per user"}})
	readback(t, adopted, "widget-C2 adopted; the --clause was not recorded: a reaction keeps the wording (reword it with --as refines)")
}

func attachedTo(t *testing.T, s *ledger.Store, utterance string) string {
	t.Helper()
	events, err := s.Events(project, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		var p ledger.AttachPayload
		if e.Type == ledger.EventAttach && json.Unmarshal(e.Payload, &p) == nil && p.UtteranceID == utterance {
			return p.To
		}
	}
	t.Fatalf("no attach event for %s", utterance)
	return ""
}

func TestAPlainReactionToAnythingChangesNothing(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "and exports")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"exports"}})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "the index leaks a handle"})
	for i, tc := range []struct{ to, want string }{
		{"widget-G1", "is a reaction to widget-G1 (Search); nothing changed"},
		{ledger.ProjectTarget, "is a reaction to the project; nothing changed"},
		{"widget-A1", "is a reaction to widget-A1; nothing changed"},
		{"widget-W1", "is a reaction to the project; nothing changed (found work widget-W1 hangs off no clause, so --to widget-W1 read as the project)"},
	} {
		ref := fmt.Sprintf("claude-prompt:%d", 10+i)
		capture(t, s, project, ref, "nice")
		result := attach(t, s, ledger.AttachRequest{UtteranceID: ref, As: ledger.AsReacts, To: tc.to})
		if !strings.HasPrefix(result.Readback, "intent ledger: "+ref+" "+tc.want) {
			t.Fatalf("react to %s: readback %q", tc.to, result.Readback)
		}
	}
	for _, id := range []string{"widget-C1", "widget-C2"} {
		if c := clause(t, board(t, s), id); c.State != ledger.StateCaptured || len(c.Cuts) != 0 {
			t.Fatalf("a plain reaction changed %s: %+v", id, c)
		}
	}
}

func TestClosingWithAPlainReasonIsReadAsDone(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index")
	closed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "finished in PR #12"})
	if closed.Item.State != ledger.WorkDone || closed.Readback != "intent ledger: widget-W1 done (widget-C1): finished in PR #12 (read as done; start the reason with duplicate, wontfix or obsolete to close it as not done)" || closed.Item.Reason != "done: finished in PR #12" {
		t.Fatalf("closed = %+v readback %q", closed.Item, closed.Readback)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W2", Reason: "finished it"}, "claimed work is finished through submit and review")
}
