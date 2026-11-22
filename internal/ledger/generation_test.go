package ledger_test

import (
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestTheGenerationMovesOnEveryWriteAndForgetAndNotOnReads(t *testing.T) {
	s := newStore(t)
	start := s.Generation()
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "add the index"})
	added := s.Generation()
	if added == start {
		t.Fatal("a write left the generation where it was")
	}
	if _, err := s.Board(project); err != nil {
		t.Fatal(err)
	}
	if s.Generation() != added {
		t.Fatal("a read moved the generation")
	}
	s.Forget(project)
	if s.Generation() == added {
		t.Fatal("forgetting a project's boards, as a project move does after rewriting its events, left the generation where it was")
	}
}
