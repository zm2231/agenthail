package ledger_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

var asked = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func askQuestion(t *testing.T, s *ledger.Store, req ledger.QuestionRequest) ledger.Question {
	t.Helper()
	fill(&req)
	q, err := s.AskQuestion(req, asked)
	if err != nil {
		t.Fatalf("ask %+v: %v", req, err)
	}
	return q
}

func fill(req *ledger.QuestionRequest) {
	if req.ProjectKey == "" {
		req.ProjectKey = project
	}
	if req.SessionID == "" {
		req.SessionID = "s1"
	}
	if req.Recommendation == "" {
		req.Recommendation = "full-text"
	}
	if req.DeadlineMs == 0 {
		req.DeadlineMs = asked.Add(time.Hour).UnixMilli()
	}
	if req.Kind == "" {
		req.Kind = ledger.QuestionScope
	}
}

func questionRefused(t *testing.T, s *ledger.Store, req ledger.QuestionRequest, want string) {
	t.Helper()
	fill(&req)
	_, err := s.AskQuestion(req, asked)
	var refusal *ledger.Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, want) {
		t.Fatalf("ask %+v: want refusal containing %q, got %v", req, want, err)
	}
}

func question(t *testing.T, s *ledger.Store, id string) ledger.Question {
	t.Helper()
	for _, q := range board(t, s).Questions() {
		if q.ID == id {
			return q
		}
	}
	t.Fatalf("question %s not on the board", id)
	return ledger.Question{}
}

func expire(t *testing.T, s *ledger.Store) []ledger.Expiry {
	t.Helper()
	expiries, err := s.ExpireQuestions(project, asked.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return expiries
}

func searchAsk(t *testing.T, s *ledger.Store) string {
	t.Helper()
	capture(t, s, project, "claude-prompt:1", "build search")
	g := goal(t, s, "Search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search"}})
	return g
}

func TestAQuestionNeedsItsSafeguards(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which index?", Kind: ledger.QuestionOutward, DefaultAnswer: "send it"}, `default is always "don't"`)
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "deploy the index now?", DefaultAnswer: "yes"}, "reads as an outward act")
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which index?", Recommendation: "merge the branch", DefaultAnswer: "fts"}, "reads as an outward act")
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "how broad?", DefaultAnswer: "drop fuzzy matching"}, "reads as narrowing")
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which index?"}, "needs --default")
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which index?", DefaultAnswer: "fts", DeadlineMs: asked.Add(-time.Minute).UnixMilli()}, "deadline in the future")
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C9", Text: "which index?", DefaultAnswer: "fts"}, "is not a goal, ask or clause")
	questionRefused(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which index?", DefaultAnswer: "fts", Kind: "vibes"}, "unknown --kind")
	outward := askQuestion(t, s, ledger.QuestionRequest{To: "widget-G1", Text: "publish the index to the team?", Kind: ledger.QuestionOutward})
	if outward.DefaultAnswer != ledger.OutwardDefault || outward.ID != "widget-Q1" {
		t.Fatalf("outward question = %+v", outward)
	}
	narrowing := askQuestion(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "how broad?", DefaultAnswer: "drop fuzzy matching", Narrows: true})
	if narrowing.GoalID != "widget-G1" || !narrowing.Narrows {
		t.Fatalf("narrowing question = %+v", narrowing)
	}
}

func TestAnAnswerIsThePersonsWordsAndUpdatesTheTarget(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "full-text, with typo tolerance")
	q := askQuestion(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "which kind of search?", DefaultAnswer: "full-text"})
	other := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"rank results"}})
	if !strings.Contains(other.Readback, "open questions: widget-Q1") {
		t.Fatalf("readback does not list the open question: %q", other.Readback)
	}
	refused(t, s, ledger.AttachRequest{As: ledger.AsAnswers, To: q.ID, Clauses: []string{"x"}}, "needs the person's utterance")
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsAnswers, To: q.ID, Clauses: []string{"build full-text search with typo tolerance"}})
	if result.Readback != "intent ledger: widget-Q1 answered (widget-C1 refined)" {
		t.Fatalf("readback = %q", result.Readback)
	}
	answered := question(t, s, q.ID)
	if answered.State != ledger.QuestionAnswered || !answered.Applied || answered.AnsweredBy != "claude-prompt:2" || answered.Answer != "full-text, with typo tolerance" {
		t.Fatalf("answered = %+v", answered)
	}
	if c := clause(t, board(t, s), "widget-C1"); c.Text != "build full-text search with typo tolerance" {
		t.Fatalf("clause = %+v", c)
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsAnswers, To: q.ID}, "already answered")
	if expiries := expire(t, s); len(expiries) != 0 {
		t.Fatalf("an answered question expired: %+v", expiries)
	}
}

func TestABreadthKeepingDefaultAppliesAsADisclosedDecision(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	q := askQuestion(t, s, ledger.QuestionRequest{To: "widget-G1", Text: "which kind of search?", DefaultAnswer: "full-text and fuzzy"})
	if expiries, err := s.ExpireQuestions(project, asked.Add(30*time.Minute)); err != nil || len(expiries) != 0 {
		t.Fatalf("expired before its deadline: %+v %v", expiries, err)
	}
	expiries := expire(t, s)
	if len(expiries) != 1 || expiries[0].Outcome != ledger.ExpiryDefaulted || expiries[0].Effects[0] != "widget-D1" {
		t.Fatalf("expiries = %+v", expiries)
	}
	b := board(t, s)
	d := b.Decisions()
	if len(d) != 1 || d[0].GoalID != "widget-G1" || !strings.Contains(d[0].Text, "default applied") {
		t.Fatalf("decisions = %+v", d)
	}
	if got := question(t, s, q.ID); got.State != ledger.QuestionDefaulted || got.Answer != "full-text and fuzzy" {
		t.Fatalf("question = %+v", got)
	}
	if again := expire(t, s); len(again) != 0 {
		t.Fatalf("a defaulted question expired twice: %+v", again)
	}
}

func TestANarrowingDefaultExpiresIntoAPendingCutNotADecision(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "ok, drop fuzzy")
	q := askQuestion(t, s, ledger.QuestionRequest{To: "widget-C1", Text: "how broad?", DefaultAnswer: "drop fuzzy matching", Narrows: true})
	expiries := expire(t, s)
	if len(expiries) != 1 || expiries[0].Outcome != ledger.ExpiryCut {
		t.Fatalf("expiries = %+v", expiries)
	}
	b := board(t, s)
	if len(b.Decisions()) != 0 {
		t.Fatalf("a narrowing default became a decision: %+v", b.Decisions())
	}
	c := clause(t, b, "widget-C1")
	if len(c.Cuts) != 1 || c.Cuts[0].Acked || c.Cuts[0].QuestionID != q.ID {
		t.Fatalf("clause cuts = %+v", c.Cuts)
	}
	if got := question(t, s, q.ID); got.State != ledger.QuestionOpen || !got.Escalated {
		t.Fatalf("question = %+v", got)
	}
	if again := expire(t, s); len(again) != 0 {
		t.Fatalf("an escalated question expired twice: %+v", again)
	}
	result := attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-X1", Ack: true})
	if result.Readback != "intent ledger: widget-X1 acked: widget-C1 is cut; widget-Q1 resolved with its default" {
		t.Fatalf("readback = %q", result.Readback)
	}
	if got := question(t, s, q.ID); got.State != ledger.QuestionAnswered || got.AnsweredBy != "claude-prompt:2" {
		t.Fatalf("question after ack = %+v", got)
	}
}

func TestAnsweringWithdrawsTheCutItsDefaultLeftPending(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "no, keep fuzzy")
	q := askQuestion(t, s, ledger.QuestionRequest{To: "widget-G1", Text: "how broad?", DefaultAnswer: "a smaller first version", Narrows: true})
	expire(t, s)
	cuts := board(t, s).TargetCuts()
	if len(cuts) != 1 || cuts[0].Target != "widget-G1" || cuts[0].Acked {
		t.Fatalf("target cuts = %+v", cuts)
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsAnswers, To: q.ID})
	if cuts := board(t, s).TargetCuts(); !cuts[0].Withdrawn {
		t.Fatalf("cut still pending after the answer: %+v", cuts)
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-X1", Ack: true}, "was withdrawn")
}

func TestAnOutwardDefaultNeverActsAndEscalatesOnce(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	q := askQuestion(t, s, ledger.QuestionRequest{To: ledger.ProjectTarget, Text: "publish the search beta?", Kind: ledger.QuestionOutward})
	expiries := expire(t, s)
	if len(expiries) != 1 || expiries[0].Outcome != ledger.ExpiryEscalated || len(expiries[0].Effects) != 0 {
		t.Fatalf("expiries = %+v", expiries)
	}
	b := board(t, s)
	if len(b.Decisions()) != 0 || len(b.TargetCuts()) != 0 {
		t.Fatalf("an outward default acted: decisions %+v cuts %+v", b.Decisions(), b.TargetCuts())
	}
	if got := question(t, s, q.ID); got.State != ledger.QuestionOpen || !got.Escalated {
		t.Fatalf("question = %+v", got)
	}
	if again := expire(t, s); len(again) != 0 {
		t.Fatalf("escalated twice: %+v", again)
	}
}
