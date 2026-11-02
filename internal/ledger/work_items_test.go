package ledger_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func work(t *testing.T, s *ledger.Store, req ledger.WorkRequest) ledger.WorkResult {
	t.Helper()
	if req.ProjectKey == "" {
		req.ProjectKey = project
	}
	if req.SessionID == "" {
		req.SessionID = "s1"
	}
	result, err := s.Work(req, time.Now())
	if err != nil {
		t.Fatalf("work %+v: %v", req, err)
	}
	return result
}

func workRefused(t *testing.T, s *ledger.Store, req ledger.WorkRequest, want string) {
	t.Helper()
	if req.ProjectKey == "" {
		req.ProjectKey = project
	}
	if req.SessionID == "" {
		req.SessionID = "s1"
	}
	_, err := s.Work(req, time.Now())
	var refusal *ledger.Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, want) {
		t.Fatalf("work %+v: want refusal containing %q, got %v", req, want, err)
	}
}

func TestWorkItemsHangOffLiveClauses(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "drop export")
	capture(t, s, project, "claude-prompt:3", "ok drop it")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"build export"}})
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C3", Reject: true})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsReacts, To: "widget-C2", Reject: true})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:3", As: ledger.AsReacts, To: "widget-X1", Ack: true})

	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C9", Text: "x"}, "is not a clause")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C2", Text: "x"}, "no longer in scope")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C3", Text: "x"}, "no longer in scope")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: " "}, "needs its text")
	result := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"})
	if result.Item.ID != "widget-W1" || result.Item.State != ledger.WorkOpen || result.Readback != "intent ledger: +widget-W1 on widget-C1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestWorkItemTransitionsAreGuarded(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1"}, "is open; submit needs it in_progress")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkBlock, ItemID: "widget-W1"}, "block needs --reason")
	workRefused(t, s, ledger.WorkRequest{Action: "finish", ItemID: "widget-W1"}, "unknown work action")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W9"}, "is not a work item")
	claimed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: "helper"})
	if claimed.Item.State != ledger.WorkInProgress || claimed.Item.Assignee != "helper" {
		t.Fatalf("claimed = %+v", claimed.Item)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: "other"}, "is in_progress; claim needs it open")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: "other"}, "only its assignee submits it")
	blocked := work(t, s, ledger.WorkRequest{Action: ledger.WorkBlock, ItemID: "widget-W1", Reason: "waiting on the schema", SessionID: "helper"})
	if blocked.Item.State != ledger.WorkBlocked || blocked.Item.Reason != "waiting on the schema" || blocked.Item.Assignee != "helper" {
		t.Fatalf("blocked = %+v", blocked.Item)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkUnblock, ItemID: "widget-W1", SessionID: "helper"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: "helper"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: "helper"})
	done := work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", SessionID: "reviewer"})
	if done.Item.State != ledger.WorkDone || done.Item.Reviewer != "reviewer" || done.Readback != "intent ledger: widget-W1 done (widget-C1) reviewed by reviewer" {
		t.Fatalf("done = %+v", done)
	}
	if reopened := work(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W1"}); reopened.Item.State != ledger.WorkOpen {
		t.Fatalf("reopened = %+v", reopened.Item)
	}
}

func TestClosingEveryWorkItemNeverClosesTheClause(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	for _, text := range []string{"add the index", "wire the endpoint"} {
		item := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: text}).Item
		work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: item.ID})
		work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: item.ID})
		work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: item.ID, SessionID: "reviewer"})
	}
	b := board(t, s)
	c := clause(t, b, "widget-C1")
	if c.State != ledger.StateCaptured || len(c.WorkItems) != 2 {
		t.Fatalf("clause after all work closed = %+v", c)
	}
	for _, item := range c.WorkItems {
		if item.State != ledger.WorkDone {
			t.Fatalf("work item = %+v", item)
		}
	}
	if open := b.ClausesOwnedBy("s1"); len(open) != 1 || len(b.OpenAsks()) != 1 {
		t.Fatalf("closing work closed the clause: owned %+v asks %+v", open, b.OpenAsks())
	}
}
