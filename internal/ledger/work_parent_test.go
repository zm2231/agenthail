package ledger_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func treeIDs(nodes []ledger.WorkTree) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.ID)
		for _, child := range treeIDs(n.Children) {
			out = append(out, n.ID+">"+child)
		}
	}
	return out
}

func TestAParentWaitsOnItsChildrenAndNamesTheOpenOnes(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	searchAsk(t, s)
	items(t, s, "ship search")
	first := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "schema", Fields: ledger.WorkFields{Parent: "widget-W1"}})
	if first.Item.ClauseID != "widget-C1" || first.Item.Parent != "widget-W1" || first.Readback != "intent ledger: +widget-W2 on widget-C1, a child of widget-W1" {
		t.Fatalf("a child is added on its parent's clause: %+v %q", first.Item, first.Readback)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "index", Fields: ledger.WorkFields{Parent: "widget-W1"}})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"})

	var ready []string
	for _, item := range board(t, s).Ready() {
		ready = append(ready, item.ID)
	}
	if !slices.Equal(ready, []string{"widget-W3"}) {
		t.Fatalf("ready = %v; a parent with open children is not ready, its open child is", ready)
	}
	open := "widget-W1 has 2 open children: widget-W2 in_progress, widget-W3 open"
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: "lead"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: "lead"}, open)
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkDelete, ItemID: "widget-W1"}, open)
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "obsolete: replaced"}, open)

	work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W3", Reason: "wontfix: not needed"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: "lead"}, "widget-W1 has 1 open child: widget-W2 in_progress")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W2"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W2", SessionID: "reviewer"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1", SessionID: "lead"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", SessionID: "reviewer"})

	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W3"}, "widget-W3 is a child of widget-W1, which is done; reopen widget-W1 before you reopen widget-W3")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "late", Fields: ledger.WorkFields{Parent: "widget-W1"}}, "widget-W1 is done; reopen it before giving it children")

	ctx, _ := board(t, s).WorkContext("widget-W1")
	if len(ctx.Children) != 2 || ctx.Children[0].ID != "widget-W2" || ctx.Children[1].State != ledger.WorkClosed {
		t.Fatalf("show lists the children with their states: %+v", ctx.Children)
	}
	replayed, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := replayed.WorkItem("widget-W1"); !slices.Equal(again.Children, []string{"widget-W2", "widget-W3"}) {
		t.Fatalf("a board replayed from the log has children %v", again.Children)
	}
}

func TestMovingAnItemUnderAnotherRefusesCyclesAndTheTreeFollows(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "epic", "step", "other")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W2", Change: ledger.WorkChange{Parent: ptr("widget-W1")}})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W1", Change: ledger.WorkChange{Parent: ptr("widget-W2")}}, "would be a cycle")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W1", Change: ledger.WorkChange{Parent: ptr("widget-W1")}}, "cannot be its own parent")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"}, "that would be a cycle")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W2", Change: ledger.WorkChange{Parent: ptr("widget-W1")}}, "already a child of widget-W1")

	tree, _ := board(t, s).Tree("", false)
	if got := treeIDs(tree); !slices.Equal(got, []string{"widget-W1", "widget-W1>widget-W2", "widget-W3"}) {
		t.Fatalf("tree = %v", got)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W2", Change: ledger.WorkChange{Parent: ptr("widget-W3")}})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W3", Change: ledger.WorkChange{Parent: ptr("")}}, "widget-W3 has no parent")
	tree, _ = board(t, s).Tree("", false)
	if got := treeIDs(tree); !slices.Equal(got, []string{"widget-W1", "widget-W3", "widget-W3>widget-W2"}) {
		t.Fatalf("tree after the move = %v", got)
	}
	if w1, _ := board(t, s).WorkItem("widget-W1"); len(w1.Children) != 0 {
		t.Fatalf("the old parent keeps %v", w1.Children)
	}
	var path []string
	for _, item := range board(t, s).CriticalPath() {
		path = append(path, item.ID)
	}
	if !slices.Equal(path, []string{"widget-W2", "widget-W3"}) {
		t.Fatalf("critical path = %v; a child comes before its parent", path)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W2", Change: ledger.WorkChange{Parent: ptr("")}})
	tree, _ = board(t, s).Tree("", false)
	if got := treeIDs(tree); !slices.Equal(got, []string{"widget-W1", "widget-W2", "widget-W3"}) {
		t.Fatalf("tree after taking widget-W2 out = %v", got)
	}
}

func TestADependencyCanBeRemoved(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkUndep, ItemID: "widget-W1", DependsOn: "widget-W2"}, "widget-W1 does not depend on widget-W2")
	removed := work(t, s, ledger.WorkRequest{Action: ledger.WorkUndep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	if len(removed.Item.DependsOn) != 0 {
		t.Fatalf("after undep = %+v", removed.Item)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W2"})
}

func ptr(s string) *string { return &s }

func TestAnImportedDependencyThatWouldCloseALoopThroughAParentIsSkipped(t *testing.T) {
	s := newStore(t)
	imported, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1",
		Items:   []ledger.WorkImport{{ImportedFrom: "td:td-epic", Text: "epic"}, {ImportedFrom: "td:td-step", Text: "step"}},
		Parents: []ledger.WorkImportParent{{Item: "td:td-step", Parent: "td:td-epic"}}}, time.Now())
	if err != nil || imported.Parents != 1 {
		t.Fatalf("import = %+v, %v", imported, err)
	}
	again, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1",
		Deps: []ledger.WorkImportDep{{Item: "td:td-step", On: "td:td-epic"}}}, time.Now())
	if err != nil || again.Deps != 0 || len(again.Skipped) != 1 || !strings.Contains(again.Skipped[0].Reason, "would be a cycle") {
		t.Fatalf("a child waiting on its parent was imported: %+v, %v", again, err)
	}
	if path := board(t, s).CriticalPath(); len(path) != 2 {
		t.Fatalf("critical path = %+v", path)
	}
}
