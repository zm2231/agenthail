package ledger_test

import (
	"slices"
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
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "done already"}, "close needs --reason")
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
