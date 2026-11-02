package ledger_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func checkRefused(t *testing.T, err error, want string) {
	t.Helper()
	var refusal *ledger.Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, want) {
		t.Fatalf("want refusal containing %q, got %v", want, err)
	}
}

func boardOf(t *testing.T, s *ledger.Store) *ledger.Board {
	t.Helper()
	b, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fuzzySearch(t *testing.T, s *ledger.Store) string {
	t.Helper()
	g := searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "and make it fuzzy, with typo tolerance")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"fuzzy matching"}})
	return g
}

func TestACheckerCutIsCoveredByTheProposerOrAckedByThePerson(t *testing.T) {
	s := newStore(t)
	g := fuzzySearch(t, s)
	now := time.Now()
	if due := boardOf(t, s).DueChecks(now, 5*time.Minute); len(due) != 0 {
		t.Fatalf("a goal changed just now is due: %v", due)
	}
	if due := boardOf(t, s).DueChecks(now.Add(6*time.Minute), 5*time.Minute); !slices.Equal(due, []string{g}) {
		t.Fatalf("due after the idle window = %v", due)
	}
	in, err := s.StartCheck(project, g, "run-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Utterances) != 2 || len(in.Clauses) != 2 {
		t.Fatalf("check input = %+v", in)
	}
	if due := boardOf(t, s).DueChecks(now.Add(time.Hour), 5*time.Minute); len(due) != 0 {
		t.Fatalf("a goal with a check running is due: %v", due)
	}
	_, err = s.StartCheck(project, g, "run-2", now)
	checkRefused(t, err, "already has a check running")
	report := func(r ledger.CheckReport) (ledger.CheckOutcome, error) {
		r.ProjectKey = project
		if r.Run == "" {
			r.Run = "run-1"
		}
		if r.SessionID == "" {
			r.SessionID = "run-1"
		}
		return s.ReportCheck(r, now)
	}
	_, err = report(ledger.CheckReport{SessionID: "s1", None: true})
	checkRefused(t, err, "only the checker session")
	_, err = report(ledger.CheckReport{Missing: []ledger.Omission{{Breadth: "typo tolerance", From: "claude-prompt:9"}}})
	checkRefused(t, err, "not one of the words this check was given")
	_, err = report(ledger.CheckReport{})
	checkRefused(t, err, "either --none or at least one --missing")
	outcome, err := report(ledger.CheckReport{Missing: []ledger.Omission{{Breadth: "typo tolerance", From: "claude-prompt:2"}}})
	if err != nil || outcome.Result != ledger.CheckReported || !slices.Equal(outcome.Cuts, []string{"widget-X1"}) {
		t.Fatalf("report = %+v, %v", outcome, err)
	}
	_, err = report(ledger.CheckReport{None: true})
	checkRefused(t, err, "already finished")
	b := boardOf(t, s)
	if checks := b.GoalChecks(); len(checks) != 1 || checks[0].Runs != 1 || checks[0].CutsRaised != 1 {
		t.Fatalf("goal checks = %+v", checks)
	}
	if due := b.DueChecks(now.Add(time.Hour), 5*time.Minute); len(due) != 0 {
		t.Fatalf("the checker's own cut made the goal due: %v", due)
	}

	cover := ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"typo tolerance"}, Covers: []string{"widget-X1"}}
	fromElsewhere := cover
	fromElsewhere.UtteranceID = "claude-prompt:1"
	refused(t, s, fromElsewhere, "raised from claude-prompt:2")
	proposal := cover
	proposal.Source = ledger.SourceProposed
	refused(t, s, proposal, "stated or inferred clause")
	conversation := ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsConversation, Covers: []string{"widget-X1"}}
	refused(t, s, conversation, "--covers apply only to new clauses")
	covered := attach(t, s, cover)
	if !slices.Contains(covered.Effects, "widget-X1 covered") {
		t.Fatalf("cover effects = %v", covered.Effects)
	}
	cuts := boardOf(t, s).TargetCuts()
	if len(cuts) != 1 || !cuts[0].Withdrawn || !slices.Equal(cuts[0].CoveredBy, []string{"widget-C3"}) {
		t.Fatalf("covered cut = %+v", cuts)
	}
	refused(t, s, cover, "already withdrawn")
	ack := ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-X1", Ack: true}
	refused(t, s, ack, "covered by widget-C3")
	if due := boardOf(t, s).DueChecks(time.Now().Add(6*time.Minute), 5*time.Minute); !slices.Equal(due, []string{g}) {
		t.Fatalf("covering changed the goal, so it is due again: %v", due)
	}
}

func TestAnUnfinishedCheckIsFinishedOnce(t *testing.T) {
	s := newStore(t)
	g := fuzzySearch(t, s)
	now := time.Now()
	if _, err := s.StartCheck(project, g, "run-1", now); err != nil {
		t.Fatal(err)
	}
	if open := boardOf(t, s).OpenChecks(); len(open) != 1 || open[0].SessionID != "run-1" || open[0].GoalID != g {
		t.Fatalf("open checks = %+v", open)
	}
	if err := s.FinishCheck(project, "run-1", ledger.CheckTimedOut, "no report in 10m", now); err != nil {
		t.Fatal(err)
	}
	before, err := s.Events(project, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishCheck(project, "run-1", ledger.CheckInterrupted, "", now); err != nil {
		t.Fatal(err)
	}
	after, err := s.Events(project, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("finishing a finished check wrote again")
	}
	run, ok := boardOf(t, s).CheckRun("run-1")
	if !ok || run.Result != ledger.CheckTimedOut || len(boardOf(t, s).OpenChecks()) != 0 {
		t.Fatalf("run = %+v", run)
	}
	_, err = s.ReportCheck(ledger.CheckReport{ProjectKey: project, Run: "run-1", SessionID: "run-1", None: true}, now)
	checkRefused(t, err, "already finished (timed-out)")
	_, err = s.StartCheck(project, g, "run-1", now)
	checkRefused(t, err, "already recorded")
}
