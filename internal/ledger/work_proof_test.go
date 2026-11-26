package ledger_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func proofWrite(op, operation string, step ledger.ProofStep, base int) ledger.WorkRequest {
	return ledger.WorkRequest{Action: ledger.WorkProof, ItemID: "widget-W1", SessionID: holder, Proof: &ledger.ProofWrite{Op: op, OperationID: operation, Step: step, Base: base}}
}

var importChecked = ledger.ProofStep{Title: "Import the March export", Expected: "every row lands, none twice", Observed: "4,112 rows, no duplicates", Outcome: ledger.ProofPass,
	Revision: "45a6bdafb", Attachments: []ledger.ProofLink{{Title: "run log", URL: "https://ci.example.com/runs/12"}}}

func proofOf(t *testing.T, s *ledger.Store) []ledger.ProofStep {
	t.Helper()
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := board.WorkItem("widget-W1")
	return item.Proof
}

func TestTheHolderRecordsProofAndRewritesItOnlyAtTheVersionItRead(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "import the March export")

	added := work(t, s, proofWrite(ledger.ProofAdd, "op-1", importChecked, 0))
	if len(added.Item.Proof) != 1 || added.Item.Proof[0].ID != "p1" || added.Item.Proof[0].Version != 1 || added.Item.Proof[0].By != holder || added.Item.ProofCounts() != (ledger.ProofCounts{Pass: 1}) {
		t.Fatalf("added = %+v", added.Item.Proof)
	}
	again := work(t, s, proofWrite(ledger.ProofAdd, "op-1", importChecked, 0))
	if len(again.Item.Proof) != 1 {
		t.Fatalf("a retried add recorded %d steps", len(again.Item.Proof))
	}
	work(t, s, proofWrite(ledger.ProofAdd, "op-2", ledger.ProofStep{Title: "Search finds an imported order", Expected: "the order shows in under a second"}, 0))

	failed := importChecked
	failed.ID, failed.Outcome, failed.Observed = "p1", ledger.ProofFail, "12 rows twice"
	workRefused(t, s, proofWrite(ledger.ProofReplace, "op-3", failed, 2), "at version 1, not 2")
	replaced := work(t, s, proofWrite(ledger.ProofReplace, "op-4", failed, 1))
	if step := replaced.Item.Proof[0]; step.Version != 2 || step.Outcome != ledger.ProofFail || replaced.Item.ProofCounts() != (ledger.ProofCounts{Fail: 1, Pending: 1}) {
		t.Fatalf("replaced = %+v", replaced.Item.Proof)
	}
	workRefused(t, s, proofWrite(ledger.ProofRemove, "op-5", ledger.ProofStep{ID: "p1"}, 1), "at version 2, not 1")
	work(t, s, proofWrite(ledger.ProofRemove, "op-6", ledger.ProofStep{ID: "p1"}, 2))
	third := work(t, s, proofWrite(ledger.ProofAdd, "op-7", importChecked, 0))
	if ids := []string{third.Item.Proof[0].ID, third.Item.Proof[1].ID}; ids[0] != "p2" || ids[1] != "p3" {
		t.Fatalf("after removing p1 the steps are %v, want p2 then a new p3", ids)
	}

	fresh := proofOf(t, s)
	if fresh[0].Version != 1 || fresh[1].Version != 1 || fresh[1].ID != "p3" {
		t.Fatalf("proof read back = %+v", fresh)
	}
}

func TestOnlyTheHolderWritesProofAndAWellFormedStep(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "import the March export")
	other := proofWrite(ledger.ProofAdd, "other", importChecked, 0)
	other.SessionID = "reviewer"
	workRefused(t, s, other, "only the session holding widget-W1")

	bad := []struct {
		step ledger.ProofStep
		want string
	}{
		{ledger.ProofStep{Expected: "x"}, "needs a title"},
		{ledger.ProofStep{Title: "x"}, "needs expected"},
		{ledger.ProofStep{Title: "x", Expected: "y", Outcome: "maybe"}, "not pass, fail or pending"},
		{ledger.ProofStep{Title: "x", Expected: "y", Outcome: ledger.ProofPass}, "says what was observed"},
		{ledger.ProofStep{Title: "x", Expected: "y", Attachments: []ledger.ProofLink{{Title: "shot", URL: "file:///srv/shots/import.png"}}}, "http(s) link"},
		{ledger.ProofStep{Title: "x", Expected: "y", Revision: "two words"}, "one word"},
	}
	for i, c := range bad {
		workRefused(t, s, proofWrite(ledger.ProofAdd, fmt.Sprint("bad-", i), c.step, 0), c.want)
	}
	workRefused(t, s, proofWrite(ledger.ProofAdd, "", importChecked, 0), "operationId")
	workRefused(t, s, proofWrite("edit", "odd", importChecked, 0), "not add, replace or remove")
	for i := range 30 {
		work(t, s, proofWrite(ledger.ProofAdd, fmt.Sprint("fill-", i), importChecked, 0))
	}
	workRefused(t, s, proofWrite(ledger.ProofAdd, "one-more", importChecked, 0), "already has 30 proof steps")
}

func TestProofFromBeforeASendBackIsStale(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "import the March export")
	work(t, s, proofWrite(ledger.ProofAdd, "before", importChecked, 0))
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: holder})
	if proof := proofOf(t, s); proof[0].Stale {
		t.Fatalf("submitting made the proof stale: %+v", proof)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReject, ItemID: "widget-W1", SessionID: "reviewer", Reason: "duplicates on the second run"})
	work(t, s, proofWrite(ledger.ProofAdd, "after", importChecked, 0))
	proof := proofOf(t, s)
	if !proof[0].Stale || proof[1].Stale {
		t.Fatalf("after a send-back, proof = %+v; want the first step stale and the new one current", proof)
	}
	board, _ := s.Board(project)
	item, _ := board.WorkItem("widget-W1")
	if item.ProofCounts() != (ledger.ProofCounts{Pass: 1}) {
		t.Fatalf("counts = %+v, want only the current step", item.ProofCounts())
	}
}

func TestAReplicaReplayingTheOwnersProofEventsReadsTheSameVersions(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	searchAsk(t, s)
	heldBy(t, s, "import the March export")
	work(t, s, proofWrite(ledger.ProofAdd, "a", importChecked, 0))
	changed := importChecked
	changed.ID = "p1"
	work(t, s, proofWrite(ledger.ProofReplace, "b", changed, 1))
	events, err := s.Events(project, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	carried := 0
	for _, e := range events {
		if e.Type == ledger.EventWorkProof && strings.Contains(string(e.Payload), `"version":`) {
			carried++
		}
	}
	if carried != 2 {
		t.Fatalf("%d proof events carry their step's version, want both", carried)
	}
	replayed, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := replayed.WorkItem("widget-W1")
	if len(item.Proof) != 1 || item.Proof[0].Version != 2 {
		t.Fatalf("a board built from the events reads %+v", item.Proof)
	}
}
