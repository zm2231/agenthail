package ledger_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func passOn(s *ledger.Store, reason string) error {
	_, err := s.Work(ledger.WorkRequest{ProjectKey: project, SessionID: "lead", Action: ledger.WorkReviewPass, ItemID: "widget-W1", Reason: reason}, time.Now())
	return err
}

func passOf(t *testing.T, s *ledger.Store) *ledger.ReviewPass {
	t.Helper()
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := board.WorkItem("widget-W1")
	return item.ReviewPass
}

func TestALeadPassesAReviewOnOnlyWhileTheItemIsInReviewAndThePassEndsWithTheReview(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "import the March export")

	if err := passOn(s, "the person decides the import window"); err == nil || !strings.Contains(err.Error(), "in_review") {
		t.Fatalf("a pass on an item still in progress = %v", err)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: holder})
	if err := passOn(s, " "); err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("a pass without a reason = %v", err)
	}
	if err := passOn(s, "the person decides the import window"); err != nil {
		t.Fatal(err)
	}
	if pass := passOf(t, s); pass == nil || pass.By != "lead" || pass.Reason != "the person decides the import window" {
		t.Fatalf("pass = %+v", pass)
	}
	if err := passOn(s, "again"); err == nil || !strings.Contains(err.Error(), "already passed") {
		t.Fatalf("a second pass = %v", err)
	}

	work(t, s, ledger.WorkRequest{Action: ledger.WorkReject, ItemID: "widget-W1", SessionID: "lead", Reason: "the totals are off"})
	if pass := passOf(t, s); pass != nil {
		t.Fatalf("an item sent back kept its pass: %+v", pass)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: holder})
	if pass := passOf(t, s); pass != nil {
		t.Fatalf("an item submitted again came back passed: %+v", pass)
	}
	if err := passOn(s, "the second round is the person's too"); err != nil {
		t.Fatal(err)
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
	if item, _ := rebuilt.WorkItem("widget-W1"); item.ReviewPass == nil || item.ReviewPass.Reason != "the second round is the person's too" {
		t.Fatalf("a board rebuilt from the events reads %+v", item.ReviewPass)
	}
}
