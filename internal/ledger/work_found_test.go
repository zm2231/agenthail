package ledger_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func clauseWork(t *testing.T, s *ledger.Store, clauseID string) []string {
	t.Helper()
	var ids []string
	for _, ask := range board(t, s).AllAsks() {
		for _, c := range ask.Clauses {
			if c.ID == clauseID {
				for _, item := range c.WorkItems {
					ids = append(ids, item.ID)
				}
			}
		}
	}
	return ids
}

func TestFoundWorkStaysOutOfThePersonsIntentUntilLinked(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index")
	found := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "the index build leaks a file handle",
		Fields: ledger.WorkFields{Type: "Bug", Priority: "1", Labels: []string{"index, perf", "index"}, From: "widget-W1", Description: "seen in the test run"}})
	item := found.Item
	if item.ID != "widget-W2" || item.ClauseID != "" || item.Type != ledger.WorkBug || item.Priority != "P1" || !slices.Equal(item.Labels, []string{"index", "perf"}) || item.From != "widget-W1" {
		t.Fatalf("found item = %+v", item)
	}
	if got := clauseWork(t, s, "widget-C1"); !slices.Equal(got, []string{"widget-W1"}) {
		t.Fatalf("found work entered the person's clause: %v", got)
	}
	ctx, ok := board(t, s).WorkContext("widget-W2")
	if !ok || ctx.Clause != nil || ctx.FoundIn == nil || ctx.FoundIn.Work == nil || ctx.FoundIn.Work.ID != "widget-W1" {
		t.Fatalf("found item context = %+v", ctx)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "x", Fields: ledger.WorkFields{From: "widget-W9"}}, "is not a work item or clause")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "x", Fields: ledger.WorkFields{Priority: "P7"}}, "unknown priority")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "x", ClauseID: "widget-C1", Fields: ledger.WorkFields{From: "widget-W1"}}, "give one")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkLink, ItemID: "widget-W2", ClauseID: "widget-C9"}, "is not a clause")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLink, ItemID: "widget-W2", ClauseID: "widget-C1"})
	if got := clauseWork(t, s, "widget-C1"); !slices.Equal(got, []string{"widget-W1", "widget-W2"}) {
		t.Fatalf("linked work is not under the clause: %v", got)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkLink, ItemID: "widget-W2", ClauseID: "widget-C1"}, "only found work is linked")
	ctx, _ = board(t, s).WorkContext("widget-W2")
	if ctx.Clause == nil || ctx.Clause.Text != "build search" || len(ctx.Clause.Words) != 1 || ctx.Clause.Words[0].Text != "build search" || ctx.Clause.Goal != "Search" {
		t.Fatalf("linked item context does not carry the person's words: %+v", ctx.Clause)
	}
}

func TestFoundWorkLinkedToRetractedWordsGoesBackToFound(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "flaky test in search"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLink, ItemID: "widget-W1", ClauseID: "widget-C1"})
	if done, err := s.ReclassifyUtterance(project, "claude-prompt:1", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	item, ok := board(t, s).WorkItem("widget-W1")
	if !ok || item.ClauseID != "" {
		t.Fatalf("found work linked to words that were not the person's = %+v %v", item, ok)
	}
}

func TestClosingDeletingAndRestoringWork(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1"}, "close needs --reason")
	closed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "duplicate of widget-W9"})
	if closed.Item.State != ledger.WorkClosed {
		t.Fatalf("closed = %+v", closed.Item)
	}
	if ready := board(t, s).Ready(); len(ready) != 1 || ready[0].ID != "widget-W2" {
		t.Fatalf("closing a dependency did not free its dependent: %+v", ready)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDelete, ItemID: "widget-W2"})
	if ready := board(t, s).Ready(); len(ready) != 1 || ready[0].ID != "widget-W1" {
		t.Fatalf("a deleted item is still ready: %+v", ready)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"}, "is deleted")
	restored := work(t, s, ledger.WorkRequest{Action: ledger.WorkRestore, ItemID: "widget-W2"})
	if restored.Item.State != ledger.WorkOpen {
		t.Fatalf("restored = %+v", restored.Item)
	}
}

func TestUnclaimedWorkClosesAsDoneWithWhatFinishedIt(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index", "ranking")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "done"}, "needs what finished")
	closed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "done: PR #12"})
	if closed.Item.State != ledger.WorkDone || closed.Item.Reason != "done: PR #12" || closed.Item.Reviewer != "" || !strings.Contains(closed.Readback, "PR #12") {
		t.Fatalf("closed as done = %+v %q", closed, closed.Readback)
	}
	if ready := board(t, s).Ready(); len(ready) != 2 || ready[0].ID != "widget-W2" {
		t.Fatalf("a done dependency did not free its dependent: %+v", ready)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W2", Reason: "done: PR #13"}, "submit and review")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkBlock, ItemID: "widget-W2", Reason: "waits on CI"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W2", Reason: "done: PR #13"}, "submit and review")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W3"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W3"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W3", Reason: "done: PR #14"}, "submit and review")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W3", SessionID: "reviewer"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W3"})
	if again := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W3", Reason: "done: PR #15"}); again.Item.State != ledger.WorkDone || again.Item.Reviewer != "" {
		t.Fatalf("a done close kept an earlier review: %+v", again.Item)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W3"})
	if closed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W3", Reason: "obsolete, superseded"}); closed.Item.State != ledger.WorkClosed {
		t.Fatalf("closed as obsolete = %+v", closed.Item)
	}
	if closed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W2", Reason: "wontfix"}); closed.Item.State != ledger.WorkClosed {
		t.Fatalf("a claimed item can still be closed as wontfix: %+v", closed.Item)
	}
}

func TestEveryWorkActionTakesAnImportedItemsTDID(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	if _, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1", Items: []ledger.WorkImport{
		{ImportedFrom: "td:td-aa11", Text: "schema"}, {ImportedFrom: "td:td-bb22", Text: "index"}, {ImportedFrom: "td:widget-C1", Text: "named like a clause"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "td-bb22", DependsOn: "td:td-aa11"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "td-aa11", Text: "landed in main"})
	found := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "flaky test", Fields: ledger.WorkFields{From: "td-aa11"}})
	closed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "td-aa11", Reason: "done: commit 1a2b3c"})
	if closed.Item.ID != "widget-W1" || closed.Item.State != ledger.WorkDone {
		t.Fatalf("closed by td id = %+v", closed.Item)
	}
	ctx, ok := board(t, s).WorkContext("td-bb22")
	if !ok || len(ctx.DependsOn) != 1 || ctx.DependsOn[0].ID != "widget-W1" || found.Item.From != "widget-W1" {
		t.Fatalf("td ids were stored instead of item ids: %+v %+v", ctx, found.Item)
	}
	if ctx, _ := board(t, s).WorkContext("td-aa11"); len(ctx.Dependents) != 1 || ctx.Dependents[0].ID != "widget-W2" {
		t.Fatalf("an item shown by its td id lost what waits on it: %+v", ctx.Dependents)
	}
	if clause := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "from the clause", Fields: ledger.WorkFields{From: "widget-C1"}}); clause.Item.From != "widget-C1" {
		t.Fatalf("a td id shadowed a clause: %+v", clause.Item)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "td-bb"}, "is not a work item")
}

func TestReadyPutsTheMostUrgentFirst(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	for _, p := range []string{"P3", "", "P0", "P1"} {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "item " + p, Fields: ledger.WorkFields{Priority: p}})
	}
	var order []string
	for _, item := range board(t, s).Ready() {
		order = append(order, item.Priority)
	}
	if !slices.Equal(order, []string{"P0", "P1", "P2", "P3"}) {
		t.Fatalf("ready order = %v", order)
	}
}

func TestEditingWorkKeepsWhatChanged(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "slow query", Fields: ledger.WorkFields{Labels: []string{"db"}}})
	text, priority, typ := "slow search query", "P0", ledger.WorkBug
	work(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W1", Change: ledger.WorkChange{Text: &text, Priority: &priority, Type: &typ, AddLabels: []string{"search"}, RemoveLabels: []string{"db"}}})
	item, _ := board(t, s).WorkItem("widget-W1")
	if item.Text != text || item.Priority != "P0" || item.Type != ledger.WorkBug || !slices.Equal(item.Labels, []string{"search"}) {
		t.Fatalf("edited item = %+v", item)
	}
	if last := item.Timeline[len(item.Timeline)-1]; last.Kind != "edit" || last.Text == "" {
		t.Fatalf("the edit left no entry: %+v", item.Timeline)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W1"}, "edit needs at least one")
	bad := "P9"
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W1", Change: ledger.WorkChange{Priority: &bad}}, "unknown priority")
}
