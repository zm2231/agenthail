package ledger_test

import (
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func items(t *testing.T, s *ledger.Store, texts ...string) {
	t.Helper()
	for _, text := range texts {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: text})
	}
}

func TestReviewNeedsSomeoneOtherThanTheAuthor(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "looks good, ship it")
	items(t, s, "add the index", "wire the endpoint")
	for _, id := range []string{"widget-W1", "widget-W2"} {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id})
		work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: id})
	}
	capture(t, s, project, "claude-prompt:3", "the index looks right, approve it")
	elsewhere := said("claude-prompt:4", "approve everything")
	elsewhere.ProjectKey, elsewhere.TargetID = project, "other-session"
	if _, _, err := s.RecordUtterance(elsewhere); err != nil {
		t.Fatal(err)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1"}, "another session reviews it, or the person")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:404"}, "not a captured person utterance")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:2"}, "was said before widget-W1 went into review")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:4"}, "was said to other-session")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkReject, ItemID: "widget-W1", SessionID: "reviewer"}, "reject needs --reason")
	byPerson := work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:3"})
	if byPerson.Item.State != ledger.WorkDone || byPerson.Item.Reviewer != "person" {
		t.Fatalf("approved by the person = %+v", byPerson.Item)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W2", UtteranceID: "claude-prompt:3"}, "already recorded the person's review of widget-W1")
	rejected := work(t, s, ledger.WorkRequest{Action: ledger.WorkReject, ItemID: "widget-W2", SessionID: "reviewer", Reason: "missing tests"})
	if rejected.Item.State != ledger.WorkInProgress || rejected.Item.Assignee != "s1" || rejected.Item.Reason != "missing tests" {
		t.Fatalf("rejected = %+v", rejected.Item)
	}
}

func TestDependenciesGateClaimsAndRefuseCycles(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index", "endpoint", "docs")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W3", DependsOn: "widget-W2"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W1", DependsOn: "widget-W3"}, "that would be a cycle")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W1", DependsOn: "widget-W1"}, "cannot depend on itself")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"}, "already depends on")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"}, "widget-W2 waits on widget-W1")
	var path []string
	for _, item := range board(t, s).CriticalPath() {
		path = append(path, item.ID)
	}
	if !slices.Equal(path, []string{"widget-W1", "widget-W2", "widget-W3"}) {
		t.Fatalf("critical path = %v", path)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", SessionID: "reviewer"})
	if claimed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"}); claimed.Item.State != ledger.WorkInProgress {
		t.Fatalf("claim after the dependency was done = %+v", claimed.Item)
	}
}

func TestAHandoffRecordsItsShapeAndFreesTheItem(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkHandoff, ItemID: "widget-W1"}, "needs at least one of")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkHandoff, ItemID: "widget-W1", SessionID: "other", Handoff: ledger.Handoff{Done: []string{"x"}}}, "only its assignee hands it off")
	h := ledger.Handoff{Done: []string{"schema written"}, Remaining: []string{"backfill"}, Decisions: []string{"fts5"}, Uncertain: []string{"index size"}}
	result := work(t, s, ledger.WorkRequest{Action: ledger.WorkHandoff, ItemID: "widget-W1", Handoff: h})
	item := result.Item
	if item.State != ledger.WorkOpen || item.Assignee != "" || item.Handoff == nil || item.Handoff.By != "s1" || !slices.Equal(item.Handoff.Remaining, []string{"backfill"}) {
		t.Fatalf("handed off = %+v", item)
	}
	if next := work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: "helper"}); next.Item.Assignee != "helper" || next.Item.Handoff == nil {
		t.Fatalf("the next session lost the handoff: %+v", next.Item)
	}
}

func TestG2CountsWorkAndWaitingOnOthers(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index", "schema")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	if r := gate(t, s, "t1", quiet); r.Result != ledger.GatePass {
		t.Fatalf("claiming work under the clause is progress: %+v", r)
	}
	h := ledger.Handoff{Remaining: []string{"tune ranking"}}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkHandoff, ItemID: "widget-W1", Handoff: h})
	if r := gate(t, s, "t2", quiet); r.Result != ledger.GatePass {
		t.Fatalf("a handoff is progress: %+v", r)
	}
	if r := gate(t, s, "t3", quiet); r.Result != ledger.GateBlocked {
		t.Fatalf("no work this turn should block: %+v", r)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W1", DependsOn: "widget-W2", SessionID: "lead"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2", SessionID: "helper"})
	if r := gate(t, s, "t4", quiet); r.Result != ledger.GatePass {
		t.Fatalf("all open work waits on another session's item, so the clause is waiting: %+v", r)
	}
}
