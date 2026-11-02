package ledger_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

var quiet = time.Now().Add(time.Hour)

func gate(t *testing.T, s *ledger.Store, turn string, now time.Time) ledger.GateResult {
	t.Helper()
	result, err := s.GateTurn(project, "s1", turn, now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func clauseAction(t *testing.T, s *ledger.Store, req ledger.ClauseRequest, now time.Time) ledger.ClauseResult {
	t.Helper()
	req.ProjectKey = project
	if req.SessionID == "" {
		req.SessionID = "s1"
	}
	result, err := s.ClauseAction(req, now)
	if err != nil {
		t.Fatalf("clause action %+v: %v", req, err)
	}
	return result
}

func gateEvents(t *testing.T, s *ledger.Store, typ ledger.EventType) int {
	t.Helper()
	events, err := s.Events(project, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

func TestG2IgnoresSessionsWithNothingOpen(t *testing.T) {
	s := newStore(t)
	if r := gate(t, s, "t1", quiet); r.Result != "" || gateEvents(t, s, ledger.EventGate) != 0 {
		t.Fatalf("result %+v", r)
	}
}

func TestG2BlocksAnOwnedClauseWithoutProgressAndWaivesAfterTheCap(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	for i, turn := range []string{"t1", "t2", "t3"} {
		r := gate(t, s, turn, quiet.Add(time.Duration(i)*time.Minute))
		if r.Result != ledger.GateBlocked || !slices.Equal(r.Blocked, []string{"widget-C1"}) || len(r.Open) != 1 {
			t.Fatalf("turn %s = %+v", turn, r)
		}
	}
	r := gate(t, s, "t4", quiet.Add(4*time.Minute))
	if r.Result != ledger.GateWaived || !slices.Equal(r.Waived, []string{"widget-C1"}) || gateEvents(t, s, ledger.EventWaiver) != 1 {
		t.Fatalf("after the cap = %+v", r)
	}
	if r := gate(t, s, "t5", quiet.Add(5*time.Minute)); r.Result != ledger.GatePass || gateEvents(t, s, ledger.EventWaiver) != 1 {
		t.Fatalf("a waived clause blocked again: %+v", r)
	}
}

func TestG2PassesOnProgressOrALiveWaitForThatTurnOnly(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	clauseAction(t, s, ledger.ClauseRequest{Action: ledger.ClauseProgress, ClauseID: "widget-C1", Ref: "commit 1a2b3c"}, quiet)
	if r := gate(t, s, "t1", quiet); r.Result != ledger.GatePass {
		t.Fatalf("progress turn = %+v", r)
	}
	if r := gate(t, s, "t2", quiet.Add(time.Minute)); r.Result != ledger.GateBlocked {
		t.Fatalf("progress counted twice: %+v", r)
	}
	clauseAction(t, s, ledger.ClauseRequest{Action: ledger.ClauseWait, ClauseID: "widget-C1", Condition: "CI on #12", DeadlineMs: quiet.Add(30 * time.Minute).UnixMilli()}, quiet)
	if r := gate(t, s, "t3", quiet.Add(2*time.Minute)); r.Result != ledger.GatePass {
		t.Fatalf("live wait = %+v", r)
	}
	if r := gate(t, s, "t4", quiet.Add(time.Hour)); r.Result != ledger.GateBlocked {
		t.Fatalf("an expired wait still passed: %+v", r)
	}
}

func TestG2DefersWhileThePersonIsTalkingToTheSession(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	r := gate(t, s, "t1", time.Now())
	if r.Result != ledger.GateDeferredPerson || len(r.Blocked) != 0 {
		t.Fatalf("result %+v", r)
	}
	clauseAction(t, s, ledger.ClauseRequest{Action: ledger.ClauseProgress, ClauseID: "widget-C1", Ref: "file search.go"}, quiet)
	if r := gate(t, s, "t2", quiet); r.Result != ledger.GatePass {
		t.Fatalf("a deferred gate consumed the turn's progress window: %+v", r)
	}
}

func TestG2BlocksUnattachedWordsAndChecksEachTurnOnce(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "build search")
	first := gate(t, s, "t1", quiet)
	if first.Result != ledger.GateBlocked || !slices.Equal(first.Blocked, []string{"claude-prompt:1"}) {
		t.Fatalf("unattached = %+v", first)
	}
	again := gate(t, s, "t1", quiet.Add(time.Minute))
	if again.Result != ledger.GateBlocked || gateEvents(t, s, ledger.EventGate) != 1 {
		t.Fatalf("a turn was gated twice: %+v", again)
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsConversation})
	if r := gate(t, s, "t2", quiet.Add(2*time.Minute)); r.Result != "" {
		t.Fatalf("after attaching = %+v", r)
	}
}

func TestClauseActionsNeedTheOwnerAndARef(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed})
	refusedAction := func(req ledger.ClauseRequest, want string) {
		t.Helper()
		req.ProjectKey = project
		if req.SessionID == "" {
			req.SessionID = "s1"
		}
		if _, err := s.ClauseAction(req, quiet); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%+v: want %q, got %v", req, want, err)
		}
	}
	refusedAction(ledger.ClauseRequest{Action: ledger.ClauseProgress, ClauseID: "widget-C1", Ref: "x", SessionID: "helper"}, "owned by s1; own it first")
	refusedAction(ledger.ClauseRequest{Action: ledger.ClauseProgress, ClauseID: "widget-C1"}, "progress needs --ref")
	refusedAction(ledger.ClauseRequest{Action: ledger.ClauseWait, ClauseID: "widget-C1", Condition: "CI", DeadlineMs: quiet.Add(-time.Minute).UnixMilli()}, "--deadline in the future")
	refusedAction(ledger.ClauseRequest{Action: ledger.ClauseOwn, ClauseID: "widget-C2"}, "is a proposal")
	refusedAction(ledger.ClauseRequest{Action: "finish", ClauseID: "widget-C1"}, "unknown clause action")
	owned := clauseAction(t, s, ledger.ClauseRequest{Action: ledger.ClauseOwn, ClauseID: "widget-C1", SessionID: "helper"}, quiet)
	if owned.Clause.OwnerSessionID != "helper" || owned.Clause.State != ledger.StateOwned || owned.Readback != "intent ledger: widget-C1 owned by helper" {
		t.Fatalf("own = %+v", owned)
	}
	if r := gate(t, s, "t1", quiet); r.Result != "" {
		t.Fatalf("s1 still gated on a clause it no longer owns: %+v", r)
	}
}
