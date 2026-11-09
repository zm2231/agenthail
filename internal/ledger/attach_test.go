package ledger_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

const project = "repo:github.com/acme/widget"

func capture(t *testing.T, s *ledger.Store, projectKey, ref, text string) {
	t.Helper()
	u := said(ref, text)
	u.ProjectKey = projectKey
	if _, _, err := s.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
}

func goal(t *testing.T, s *ledger.Store, name string) string {
	t.Helper()
	g, _, err := s.EnsureGoal(project, name, "s1")
	if err != nil {
		t.Fatal(err)
	}
	return g.ID
}

func attach(t *testing.T, s *ledger.Store, req ledger.AttachRequest) ledger.AttachResult {
	t.Helper()
	if req.ProjectKey == "" {
		req.ProjectKey = project
	}
	if req.SessionID == "" {
		req.SessionID = "s1"
	}
	result, err := s.Attach(req)
	if err != nil {
		t.Fatalf("attach %+v: %v", req, err)
	}
	return result
}

func refused(t *testing.T, s *ledger.Store, req ledger.AttachRequest, want string) {
	t.Helper()
	if req.ProjectKey == "" {
		req.ProjectKey = project
	}
	if req.SessionID == "" {
		req.SessionID = "s1"
	}
	_, err := s.Attach(req)
	var refusal *ledger.Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, want) {
		t.Fatalf("attach %+v: want refusal containing %q, got %v", req, want, err)
	}
}

func board(t *testing.T, s *ledger.Store) *ledger.Board {
	t.Helper()
	b, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func clause(t *testing.T, b *ledger.Board, id string) ledger.Clause {
	t.Helper()
	for _, ask := range b.AllAsks() {
		for _, c := range ask.Clauses {
			if c.ID == id {
				return *c
			}
		}
	}
	t.Fatalf("clause %s not on the board", id)
	return ledger.Clause{}
}

func TestProjectSlug(t *testing.T) {
	for key, want := range map[string]string{
		"repo:github.com/zm2231/agenthail":          "agenthail",
		"repo:github.com/zm2231/agenthail.git":      "agenthail",
		"repo:github.com/zm2231/agenthail::apps/ui": "ui",
		"":                                          "ledger",
	} {
		if got := ledger.ProjectSlug(key); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestGoalIsCreatedOnceByName(t *testing.T) {
	s := newStore(t)
	g, created, err := s.EnsureGoal(project, "Checkout flow", "s1")
	if err != nil || !created || g.ID != "widget-G1" {
		t.Fatalf("first goal = %+v created=%v err=%v", g, created, err)
	}
	again, created, err := s.EnsureGoal(project, "checkout flow", "s2")
	if err != nil || created || again.ID != g.ID {
		t.Fatalf("same name = %+v created=%v err=%v", again, created, err)
	}
	other, _, _ := s.EnsureGoal(project, "Search", "s1")
	if other.ID != "widget-G2" {
		t.Fatalf("second goal id = %s", other.ID)
	}
}

func TestNewAskBindsStatedClausesToTheReceivingSession(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "add a cart and a receipt email")
	g := goal(t, s, "Checkout")
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"add a cart", "send a receipt email"}, SessionID: "helper"})
	if !slices.Equal(result.Effects, []string{"widget-A1", "widget-C1", "widget-C2"}) {
		t.Fatalf("effects = %v", result.Effects)
	}
	if result.GoalID != g || !strings.Contains(result.Readback, "+widget-A1 on widget-G1 (Checkout) with 2 clauses") {
		t.Fatalf("result = %+v", result)
	}
	b := board(t, s)
	c := clause(t, b, "widget-C2")
	if c.Source != ledger.SourceStated || c.OwnerSessionID != "s1" || !slices.Equal(c.Sources, []string{"claude-prompt:1"}) || c.GoalID != g {
		t.Fatalf("clause = %+v", c)
	}
	state, err := s.Attachment(project, b)
	if err != nil || len(state.Provisional) != 0 {
		t.Fatalf("provisional = %+v err=%v", state.Provisional, err)
	}
}

func TestProvenanceIsEnforcedByTheLedger(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "build search")
	g := goal(t, s, "Search")
	refused(t, s, ledger.AttachRequest{As: ledger.AsNewAsk, To: g, Clauses: []string{"x"}}, "stated clauses need the person's captured utterance")
	refused(t, s, ledger.AttachRequest{As: ledger.AsNewAsk, To: g, Clauses: []string{"x"}, Source: ledger.SourceInferred}, "inferred clauses need")
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:404", As: ledger.AsNewAsk, To: g, Clauses: []string{"x"}}, "not a captured person utterance")
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"x"}, Source: ledger.SourceAdopted}, "--as reacts --to <clause> --adopt")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search"}})
	refused(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"x"}, Source: ledger.SourceDelegated}, "delegated clauses need an ask the person delegated to s1")
	refused(t, s, ledger.AttachRequest{As: ledger.AsRefines, To: "widget-C1", Clauses: []string{"x"}}, "needs the person's utterance")
}

func TestAnUtteranceTheLedgerDidNotCaptureFromThePersonCannotBind(t *testing.T) {
	s := newStore(t)
	data, _ := json.Marshal(ledger.UtterancePayload{UtteranceID: "agent:1", Text: "do less", TargetID: "s1"})
	if _, err := s.AppendEvent(ledger.Event{ProjectKey: project, Type: ledger.EventUtterance, Payload: data}); err != nil {
		t.Fatal(err)
	}
	g := goal(t, s, "Search")
	refused(t, s, ledger.AttachRequest{UtteranceID: "agent:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"do less"}}, "not a captured person utterance")
}

func TestProposalBindsOnlyOnceThePersonAdoptsIt(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "make search fast")
	capture(t, s, project, "claude-prompt:2", "yes, cache it")
	g := goal(t, s, "Search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"make search fast"}})
	proposed := attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed, SessionID: "helper"})
	if !strings.Contains(proposed.Readback, "binds once the person adopts it") {
		t.Fatalf("readback = %q", proposed.Readback)
	}
	c := clause(t, board(t, s), "widget-C2")
	if c.Source != ledger.SourceProposed || c.OwnerSessionID != "" || c.ProposedBy != "helper" {
		t.Fatalf("proposal = %+v", c)
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C1", Adopt: true}, "only a proposed clause can be adopted")
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C2", Adopt: true, Ack: true}, "not several")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C2", Adopt: true})
	c = clause(t, board(t, s), "widget-C2")
	if c.Source != ledger.SourceAdopted || c.OwnerSessionID != "s1" || !slices.Contains(c.Sources, "claude-prompt:2") {
		t.Fatalf("adopted = %+v", c)
	}
}

func TestRejectingAProposalIsNotACut(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "make search fast")
	capture(t, s, project, "claude-prompt:2", "no, not that")
	capture(t, s, project, "claude-prompt:3", "drop the fast part")
	g := goal(t, s, "Search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"make search fast"}})
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed})
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C2", Reject: true})
	c := clause(t, board(t, s), "widget-C2")
	if c.State != ledger.StateRejected || len(c.Cuts) != 0 || result.Readback != "intent ledger: widget-C2 rejected; 1 provisional: claude-prompt:3" {
		t.Fatalf("rejected proposal = %+v readback %q", c, result.Readback)
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C2", Adopt: true}, "was rejected")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsReacts, To: "widget-C1", Reject: true})
	c = clause(t, board(t, s), "widget-C1")
	if len(c.Cuts) != 1 || c.Cuts[0].Acked || c.Cuts[0].ID != "widget-X1" {
		t.Fatalf("stated clause cut = %+v", c)
	}
}

func TestACutIsAckedOnlyByThePersonsWords(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "build search and export")
	capture(t, s, project, "claude-prompt:2", "drop export")
	capture(t, s, project, "claude-prompt:3", "ok, drop it")
	g := goal(t, s, "Search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search", "build export"}})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C2", Reject: true})
	refused(t, s, ledger.AttachRequest{As: ledger.AsReacts, To: "widget-X1", Ack: true}, "needs the person's utterance")
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsReacts, To: "widget-C2", Ack: true}, "\"widget-C2\" is not a cut")
	if open := board(t, s).ClausesOwnedBy("s1"); len(open) != 2 {
		t.Fatalf("a pending cut closed its clause: %+v", open)
	}
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsReacts, To: "widget-X1", Ack: true})
	if result.Readback != "intent ledger: widget-X1 acked: widget-C2 is cut" || result.GoalID != g {
		t.Fatalf("result = %+v", result)
	}
	b := board(t, s)
	if c := clause(t, b, "widget-C2"); !c.Cuts[0].Acked {
		t.Fatalf("cut not acked: %+v", c)
	}
	if open := b.ClausesOwnedBy("s1"); len(open) != 1 || open[0].ID != "widget-C1" {
		t.Fatalf("open clauses after ack = %+v", open)
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsReacts, To: "widget-X1", Ack: true}, "already acked")
}

func TestDelegatedClausesNeedTheDelegation(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "build search")
	capture(t, s, project, "claude-prompt:2", "you pick the details")
	g := goal(t, s, "Search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search"}})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsDelegates, To: "widget-A1", Delegate: "helper"})
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"rank by recency"}, Source: ledger.SourceDelegated, SessionID: "helper"})
	b := board(t, s)
	if c := clause(t, b, "widget-C2"); c.Source != ledger.SourceDelegated || c.OwnerSessionID != "helper" {
		t.Fatalf("delegated clause = %+v", c)
	}
	if c := clause(t, b, "widget-C1"); c.OwnerSessionID != "s1" {
		t.Fatalf("delegation moved an owned clause: %+v", c)
	}
}

func TestDecisionsAndConstraintsCanCoverTheProject(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "we use postgres everywhere, and never ship on fridays")
	g := goal(t, s, "Search")
	decided := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsDecides, To: ledger.ProjectTarget, Clauses: []string{"use postgres"}})
	if decided.Readback != "intent ledger: +widget-D1 decided on the project" {
		t.Fatalf("readback = %q", decided.Readback)
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsConstrains, To: ledger.ProjectTarget, Clauses: []string{"never ship on fridays"}})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsConstrains, To: g, Clauses: []string{"no new services"}})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsDecides, To: "widget-A9", Clauses: []string{"x"}}, `"widget-A9" is not a goal, ask, clause or work item`)
	b := board(t, s)
	if got := b.ProjectConstraints(); len(got) != 1 || got[0].OwnerSessionID != "" || got[0].Kind != ledger.ClauseKindConstraint {
		t.Fatalf("project constraints = %+v", got)
	}
	for _, goal := range b.Goals() {
		if goal.ID == g && len(goal.Constraints) != 1 {
			t.Fatalf("goal constraints = %+v", goal.Constraints)
		}
	}
	if d := b.Decisions(); len(d) != 1 || d[0].GoalID != "" || d[0].SourceUtterance != "claude-prompt:1" {
		t.Fatalf("decisions = %+v", d)
	}
}

func TestRefinementRecordsTheNewWordingAndItsSource(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "build search")
	capture(t, s, project, "claude-prompt:2", "full-text search, I mean")
	g := goal(t, s, "Search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search"}})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsRefines, To: "widget-C1"}, "exactly one --clause")
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsRefines, To: "widget-C1", Clauses: []string{"build full-text search"}})
	c := clause(t, board(t, s), "widget-C1")
	if c.Text != "build full-text search" || !slices.Equal(c.Sources, []string{"claude-prompt:1", "claude-prompt:2"}) || result.GoalID != g {
		t.Fatalf("refined = %+v result %+v", c, result)
	}
}

func TestReadbackNamesWhatIsStillUnattached(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "hi")
	capture(t, s, project, "claude-prompt:2", "build search")
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsConversation})
	if result.Readback != "intent ledger: claude-prompt:1 is conversation; 1 provisional: claude-prompt:2" {
		t.Fatalf("readback = %q", result.Readback)
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: "summarizes"}, "unknown --as")
}

func TestAnUtteranceAttachedInOneProjectIsAttachedElsewhereInTheOther(t *testing.T) {
	s := newStore(t)
	const other = "repo:github.com/acme/infra"
	capture(t, s, project, "claude-prompt:1", "fix the deploy")
	capture(t, s, other, "claude-prompt:1", "fix the deploy")
	g, _, err := s.EnsureGoal(other, "Deploy", "s1")
	if err != nil {
		t.Fatal(err)
	}
	attach(t, s, ledger.AttachRequest{ProjectKey: other, UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g.ID, Clauses: []string{"fix the deploy"}})
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsConversation}, "attached elsewhere: repo:github.com/acme/infra infra-G1")
	state, err := s.Attachment(project, board(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Provisional) != 0 || state.Elsewhere["claude-prompt:1"] != (ledger.Elsewhere{ProjectKey: other, GoalID: "infra-G1"}) {
		t.Fatalf("attachment = %+v", state)
	}
}
