package ledger_test

import (
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func importWork(t *testing.T, s *ledger.Store, req ledger.WorkImportRequest) ledger.WorkImportResult {
	t.Helper()
	req.ProjectKey, req.SessionID = project, "s1"
	result, err := s.ImportWork(req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestImportedWorkKeepsItsHistoryAndASecondImportChangesNothing(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	searchAsk(t, s)
	items(t, s, "already here")
	then := time.Date(2026, 7, 12, 20, 1, 29, 0, time.UTC)
	batch := ledger.WorkImportRequest{Items: []ledger.WorkImport{
		{ImportedFrom: "td:td-1", Text: "the index leaks", State: ledger.WorkOpen, CreatedAt: then, CreatedBy: "td:ses_a",
			Fields:   ledger.WorkFields{Type: ledger.WorkBug, Priority: "P1", Labels: []string{"Index"}, Description: "seen in CI"},
			Timeline: []ledger.WorkEntry{{At: then.Add(time.Hour), Actor: "td:ses_b", Kind: "comment", Text: "same on main"}}},
		{ImportedFrom: "td:td-2", Text: "search epic", State: ledger.WorkDone, Reviewer: "td:ses_c", CreatedAt: then},
		{ImportedFrom: "td:td-3", Text: "", CreatedAt: then},
	}}
	first := importWork(t, s, batch)
	if len(first.Imported) != 2 || first.Imported[0].ItemID != "widget-W2" || first.Imported[1].ItemID != "widget-W3" || len(first.Skipped) != 1 || first.Skipped[0].ImportedFrom != "td:td-3" {
		t.Fatalf("first import = %+v", first)
	}
	deps := importWork(t, s, ledger.WorkImportRequest{Deps: []ledger.WorkImportDep{
		{Item: "td:td-2", On: "td:td-1"}, {Item: "td:td-1", On: "td:td-2"}, {Item: "td:td-1", On: "td:td-9"}}})
	if deps.Deps != 1 || len(deps.Skipped) != 2 {
		t.Fatalf("deps = %+v", deps)
	}
	again := importWork(t, s, ledger.WorkImportRequest{Items: batch.Items[:2], Deps: []ledger.WorkImportDep{{Item: "td:td-2", On: "td:td-1"}}})
	if len(again.Imported) != 0 || len(again.Present) != 2 || again.Deps != 0 || len(again.Skipped) != 0 {
		t.Fatalf("second import = %+v", again)
	}
	if next := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: "after the import"}); next.Item.ID != "widget-W4" {
		t.Fatalf("an item added after the import is %s", next.Item.ID)
	}
	for name, b := range bothBoards(t, db, s) {
		leak, _ := b.WorkItem("widget-W2")
		if leak.ImportedFrom != "td:td-1" || leak.ClauseID != "" || leak.State != ledger.WorkOpen || leak.Type != ledger.WorkBug || leak.Priority != "P1" ||
			!slices.Equal(leak.Labels, []string{"index"}) || leak.CreatedBy != "td:ses_a" {
			t.Fatalf("%s board: imported item = %+v", name, leak)
		}
		if kinds := entryKinds(leak); kinds != "created comment imported" || !leak.Timeline[0].At.Equal(then) || leak.Timeline[1].Actor != "td:ses_b" {
			t.Fatalf("%s board: imported timeline %s %+v", name, kinds, leak.Timeline)
		}
		epic, _ := b.WorkItem("widget-W3")
		if epic.State != ledger.WorkDone || epic.Reviewer != "td:ses_c" || !slices.Equal(epic.DependsOn, []string{"widget-W2"}) {
			t.Fatalf("%s board: imported done item = %+v", name, epic)
		}
		if ready := b.Ready(); len(ready) != 3 || ready[0].ID != "widget-W2" {
			t.Fatalf("%s board: ready = %+v", name, ready)
		}
	}
}

func TestAnImportedItemCanArriveParkedForLater(t *testing.T) {
	s := newStore(t)
	revisit := time.Date(2099, 1, 15, 0, 0, 0, 0, time.UTC)
	result, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1", Items: []ledger.WorkImport{
		{ImportedFrom: "td:a", Text: "deferred", State: ledger.WorkParked, Park: &ledger.Park{Kind: ledger.ParkLater, Why: " deferred in td ", Revisit: revisit}},
		{ImportedFrom: "td:b", Text: "no why", State: ledger.WorkParked, Park: &ledger.Park{Kind: ledger.ParkLater}},
		{ImportedFrom: "td:c", Text: "usage", State: ledger.WorkParked, Park: &ledger.Park{Kind: ledger.ParkUsage, Why: "x", Usage: &ledger.ParkUsageTarget{Provider: "codex"}}},
		{ImportedFrom: "td:d", Text: "open with a park", Park: &ledger.Park{Kind: ledger.ParkLater, Why: "x"}},
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 1 || len(result.Skipped) != 3 {
		t.Fatalf("result = %+v", result)
	}
	item, ok := rebuild(t, s).WorkItem(result.Imported[0].ItemID)
	if !ok || item.State != ledger.WorkParked || item.Park == nil || item.Park.Kind != ledger.ParkLater || item.Park.Why != "deferred in td" || !item.Park.Revisit.Equal(revisit) {
		t.Fatalf("imported = %+v park %+v", item, item.Park)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkUnpark, ItemID: item.ID})
}
