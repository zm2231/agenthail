package ledger_test

import (
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestBoardReplaysDelegationAndSupersession(t *testing.T) {
	b := ledger.NewBoard()
	err := b.Rebuild([]ledger.Event{
		makeEvent(1, ledger.EventAsk, ledger.AskPayload{AskID: "A1", Type: ledger.AskBuild, Principal: "person"}),
		makeEvent(2, ledger.EventClause, ledger.ClausePayload{ClauseID: "C1", AskID: "A1", Text: "old text", State: ledger.StateOwned, OwnerSessionID: "lead"}),
		makeEvent(3, ledger.EventClause, ledger.ClausePayload{ClauseID: "C2", AskID: "A1", Text: "other", State: ledger.StateOwned, OwnerSessionID: "someone-else"}),
		makeEvent(4, ledger.EventDelegation, ledger.DelegationPayload{AskID: "A1", DelegatorSessionID: "lead", DelegateSessionID: "worker"}),
		makeEvent(5, ledger.EventSupersession, ledger.SupersessionPayload{ClauseID: "C1", OldVersion: "old text", NewVersion: "new text", SourceUtteranceID: "u-2"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	owned := b.ClausesOwnedBy("worker")
	if len(owned) != 1 || owned[0].ID != "C1" || owned[0].Text != "new text" {
		t.Fatalf("want C1 owned by worker with new text, got %+v", owned)
	}
	if len(b.ClausesOwnedBy("lead")) != 0 {
		t.Fatal("delegator still owns a clause after delegation")
	}
	if other := b.ClausesOwnedBy("someone-else"); len(other) != 1 {
		t.Fatalf("delegation moved a clause the delegator did not own: %+v", other)
	}
	if b.AppliedSeq() != 5 {
		t.Fatalf("applied seq %d, want 5", b.AppliedSeq())
	}
}
