package ledger_test

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

var recommendFirst = 0

func raiseOnWidget(t *testing.T, s *ledger.Store, askID string) string {
	t.Helper()
	id, err := s.RaiseItemQuestion(ledger.ItemQuestionRaise{ProjectKey: project, SessionID: holder, ItemID: "widget-W1", AskHostID: "host-a", AskID: askID, Title: "Which export?",
		Prompts: []ledger.ItemQuestionPrompt{{Text: "Which export do I import?", Options: []ledger.ItemQuestionOption{{Label: "March"}, {Label: "April", Does: "skips March"}}, Recommended: &recommendFirst}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func questionsOnWidget(t *testing.T, s *ledger.Store) []ledger.ItemQuestion {
	t.Helper()
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := board.WorkItem("widget-W1")
	return item.Questions
}

func TestAnAskOnAWorkItemIsRecordedOnceAndKeepsHowItEnded(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "import the March export")

	first := raiseOnWidget(t, s, "ask-1")
	if again := raiseOnWidget(t, s, "ask-1"); again != first {
		t.Fatalf("a retried raise issued %s after %s", again, first)
	}
	questions := questionsOnWidget(t, s)
	if len(questions) != 1 || !questions[0].Open() || questions[0].ID != first || questions[0].Prompts[0].Options[1].Does != "skips March" || *questions[0].Prompts[0].Recommended != 0 {
		t.Fatalf("questions = %+v", questions)
	}
	board, _ := s.Board(project)
	if item, _ := board.WorkItem("widget-W1"); len(item.OpenQuestions()) != 1 {
		t.Fatalf("open questions = %+v", item.OpenQuestions())
	}

	resolve := ledger.ItemQuestionResolve{ProjectKey: project, SessionID: holder, AskHostID: "host-a", AskID: "ask-1", Answer: "March", By: "lead", Outcome: ledger.ItemQuestionAnswered}
	for range 2 {
		if err := s.ResolveItemQuestion(resolve, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	questions = questionsOnWidget(t, s)
	if len(questions) != 1 || questions[0].Open() || questions[0].Outcome != ledger.ItemQuestionAnswered || questions[0].By != "lead" || questions[0].Answer != "March" {
		t.Fatalf("after the answer = %+v", questions)
	}

	if second := raiseOnWidget(t, s, "ask-2"); second == first {
		t.Fatalf("a second ask reused question %s", first)
	}
	if err := s.ResolveItemQuestion(ledger.ItemQuestionResolve{ProjectKey: project, SessionID: holder, AskHostID: "host-a", AskID: "ask-9", By: "person", Outcome: ledger.ItemQuestionWithdrawn}, time.Now()); err == nil {
		t.Fatal("resolving an ask with no question on any item was accepted")
	}

	rebuilt := ledger.NewBoard()
	events, err := s.Events(project, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if err := rebuilt.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	if item, _ := rebuilt.WorkItem("widget-W1"); len(item.Questions) != 2 || item.Questions[0].Outcome != ledger.ItemQuestionAnswered || !item.Questions[1].Open() {
		t.Fatalf("a board rebuilt from the events reads %+v", item.Questions)
	}
}
