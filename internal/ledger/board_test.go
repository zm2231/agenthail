package ledger_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func makeEvent(seq int64, typ ledger.EventType, payload any) ledger.Event {
	data, _ := json.Marshal(payload)
	return ledger.Event{Seq: seq, Type: typ, Payload: data, CreatedAt: time.Now()}
}

func TestBoardApplyAskAndClause(t *testing.T) {
	b := ledger.NewBoard()

	if err := b.Apply(makeEvent(1, ledger.EventAsk, ledger.AskPayload{
		AskID: "A1", Type: ledger.AskBuild, Principal: "person",
	})); err != nil {
		t.Fatal(err)
	}

	if err := b.Apply(makeEvent(2, ledger.EventClause, ledger.ClausePayload{
		ClauseID: "C1", AskID: "A1", Text: "county plans", State: ledger.StateCaptured, OwnerSessionID: "sess-1",
	})); err != nil {
		t.Fatal(err)
	}

	if err := b.Apply(makeEvent(3, ledger.EventClause, ledger.ClausePayload{
		ClauseID: "C2", AskID: "A1", Text: "state plans", State: ledger.StateCaptured, OwnerSessionID: "sess-1",
	})); err != nil {
		t.Fatal(err)
	}

	asks := b.OpenAsks()
	if len(asks) != 1 {
		t.Fatalf("expected 1 open ask, got %d", len(asks))
	}
	if len(asks[0].Clauses) != 2 {
		t.Fatalf("expected 2 clauses, got %d", len(asks[0].Clauses))
	}
	if b.AppliedSeq() != 3 {
		t.Fatalf("expected applied seq 3, got %d", b.AppliedSeq())
	}
}

func TestBoardClauseStateAdvance(t *testing.T) {
	b := ledger.NewBoard()
	b.Apply(makeEvent(1, ledger.EventAsk, ledger.AskPayload{AskID: "A1", Type: ledger.AskBuild}))
	b.Apply(makeEvent(2, ledger.EventClause, ledger.ClausePayload{ClauseID: "C1", AskID: "A1", Text: "test", State: ledger.StateCaptured, OwnerSessionID: "s1"}))

	b.Apply(makeEvent(3, ledger.EventClauseState, ledger.ClauseStatePayload{
		ClauseID: "C1", FromState: ledger.StateCaptured, ToState: ledger.StateOwned, Actor: "s1", ProgressRef: "abc123",
	}))

	owned := b.ClausesOwnedBy("s1")
	if len(owned) != 1 || owned[0].State != ledger.StateOwned {
		t.Fatalf("expected 1 owned clause in state owned, got %d", len(owned))
	}
	if !owned[0].SelfReported {
		t.Fatal("expected self-reported flag")
	}
	if owned[0].ProgressRef != "abc123" {
		t.Fatalf("expected progressRef abc123, got %s", owned[0].ProgressRef)
	}
}

func TestBoardCutAndAck(t *testing.T) {
	b := ledger.NewBoard()
	b.Apply(makeEvent(1, ledger.EventAsk, ledger.AskPayload{AskID: "A1", Type: ledger.AskBuild}))
	b.Apply(makeEvent(2, ledger.EventClause, ledger.ClausePayload{ClauseID: "C1", AskID: "A1", Text: "test", State: ledger.StateCaptured, OwnerSessionID: "s1"}))

	b.Apply(makeEvent(3, ledger.EventCut, ledger.CutPayload{CutID: "X1", ClauseID: "C1", Reason: "narrowed", Acked: false}))

	owned := b.ClausesOwnedBy("s1")
	if len(owned) != 1 {
		t.Fatalf("unacked cut should not remove clause from owned, got %d", len(owned))
	}

	b.Apply(makeEvent(4, ledger.EventCutAck, ledger.CutAckPayload{CutID: "X1", AckedBy: "person"}))

	owned = b.ClausesOwnedBy("s1")
	if len(owned) != 0 {
		t.Fatalf("acked cut should remove clause from owned, got %d", len(owned))
	}

	open := b.OpenAsks()
	if len(open) != 0 {
		t.Fatalf("fully cut ask should not be open, got %d", len(open))
	}
}

func TestBoardDecision(t *testing.T) {
	b := ledger.NewBoard()
	b.Apply(makeEvent(1, ledger.EventDecision, ledger.DecisionPayload{DecisionID: "D1", Version: 1, Text: "scope locked", Principal: "person"}))

	decisions := b.Decisions()
	if len(decisions) != 1 || decisions[0].Version != 1 {
		t.Fatalf("expected 1 decision v1, got %v", decisions)
	}

	b.Apply(makeEvent(2, ledger.EventDecisionVer, ledger.DecisionPayload{DecisionID: "D1", Version: 2, Text: "scope unlocked", Principal: "person"}))

	decisions = b.Decisions()
	if len(decisions) != 1 || decisions[0].Version != 2 || decisions[0].Text != "scope unlocked" {
		t.Fatalf("expected decision v2, got %v", decisions)
	}
}

func TestBoardRebuild(t *testing.T) {
	events := []ledger.Event{
		makeEvent(1, ledger.EventAsk, ledger.AskPayload{AskID: "A1", Type: ledger.AskBuild}),
		makeEvent(2, ledger.EventClause, ledger.ClausePayload{ClauseID: "C1", AskID: "A1", Text: "test", State: ledger.StateCaptured}),
		makeEvent(3, ledger.EventClauseState, ledger.ClauseStatePayload{ClauseID: "C1", FromState: ledger.StateCaptured, ToState: ledger.StateOwned, Actor: "s1"}),
	}
	b := ledger.NewBoard()
	if err := b.Rebuild(events); err != nil {
		t.Fatal(err)
	}
	if b.AppliedSeq() != 3 {
		t.Fatalf("expected applied seq 3, got %d", b.AppliedSeq())
	}
	if len(b.AllAsks()) != 1 {
		t.Fatalf("expected 1 ask after rebuild, got %d", len(b.AllAsks()))
	}
}

func TestBoardWait(t *testing.T) {
	b := ledger.NewBoard()
	b.Apply(makeEvent(1, ledger.EventAsk, ledger.AskPayload{AskID: "A1", Type: ledger.AskBuild}))
	b.Apply(makeEvent(2, ledger.EventClause, ledger.ClausePayload{ClauseID: "C1", AskID: "A1", Text: "test", State: ledger.StateCaptured, OwnerSessionID: "s1"}))

	b.Apply(makeEvent(3, ledger.EventWait, ledger.WaitPayload{ClauseID: "C1", SessionID: "s1", Condition: "CI pass", DeadlineMs: 1000}))

	owned := b.ClausesOwnedBy("s1")
	if len(owned) != 1 || len(owned[0].Waits) != 1 {
		t.Fatalf("expected 1 wait, got %v", owned)
	}
	if owned[0].Waits[0].Condition != "CI pass" {
		t.Fatalf("expected condition 'CI pass', got %s", owned[0].Waits[0].Condition)
	}

	b.Apply(makeEvent(4, ledger.EventWaitResolved, ledger.WaitResolvedPayload{ClauseID: "C1", SessionID: "s1", Resolution: "fired"}))

	owned = b.ClausesOwnedBy("s1")
	if owned[0].Waits[0].Resolved != "fired" {
		t.Fatalf("expected resolved=fired, got %s", owned[0].Waits[0].Resolved)
	}
}
