package ledger_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func attachRefusedWith(t *testing.T, s *ledger.Store, req ledger.AttachRequest, want string) {
	t.Helper()
	req.ProjectKey, req.SessionID = project, "s1"
	_, err := s.Attach(req)
	var refusal *ledger.Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, want) {
		t.Fatalf("attach %+v: want refusal containing %q, got %v", req, want, err)
	}
}

func TestAttachingThePersonsWordsAsAnIdeaParksNewFoundWork(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "one day, search across every project at once")
	words := ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsIdea}

	attachRefusedWith(t, s, words, "exactly one --clause")
	attachRefusedWith(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsIdea, To: "widget-G1", Clauses: []string{"cross-project search"}}, "found work under no goal")
	attachRefusedWith(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsNewAsk, Clauses: []string{"cross-project search"}, Idea: &ledger.ParkIdeaShape{Goal: "x"}}, "they go with --as idea")
	attachRefusedWith(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:404", As: ledger.AsIdea, Clauses: []string{"cross-project search"}}, "not a captured person utterance")

	words.Clauses = []string{"cross-project search"}
	words.Idea = &ledger.ParkIdeaShape{Goal: " search everywhere ", FirstTest: "one query hits two projects"}
	result := attach(t, s, words)
	if len(result.Effects) != 1 || !strings.Contains(result.Readback, "+"+result.Effects[0]+" idea, parked (the person's idea, claude-prompt:2)") {
		t.Fatalf("attach = %+v", result)
	}
	id := result.Effects[0]
	item, ok := rebuild(t, s).WorkItem(id)
	if !ok || item.Type != ledger.WorkIdea || item.State != ledger.WorkParked || item.ClauseID != "" || item.Text != "cross-project search" || item.Park == nil {
		t.Fatalf("idea = %+v", item)
	}
	if shape := item.Park.Idea; shape.UtteranceID != "claude-prompt:2" || shape.Goal != "search everywhere" || shape.FirstTest != "one query hits two projects" || item.Park.Session != "s1" {
		t.Fatalf("shape = %+v park = %+v", shape, item.Park)
	}
	attachRefusedWith(t, s, words, id+" already keeps claude-prompt:2 as an idea")
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	if entries := board.Backlog(ledger.BacklogQuery{Kinds: []ledger.ParkKind{ledger.ParkIdea}}, item.Timeline[0].At, nil); len(entries) != 1 || entries[0].Ready {
		t.Fatalf("the idea in the backlog = %+v", entries)
	}

	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "an idea", Fields: ledger.WorkFields{Type: ledger.WorkIdea}}, "keeps the person's words")
	other := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "a task"}).Item.ID
	idea := ledger.WorkIdea
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: other, Change: ledger.WorkChange{Type: &idea}}, "keeps the person's words")
	ideas, err := board.QueryWork(ledger.WorkQuery{Types: []ledger.WorkType{ledger.WorkIdea}, All: true})
	if err != nil || len(ideas) != 1 || ideas[0].ID != id {
		t.Fatalf("list --type idea = %+v, %v", ideas, err)
	}
}
