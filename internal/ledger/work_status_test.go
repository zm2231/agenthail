package ledger_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func statusIDs(rows []ledger.StatusRow) string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return strings.Join(ids, ",")
}

func TestASessionsStatusSortsItsWorkAndNamesARejection(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index", "endpoint", "docs", "theirs", "spare")
	for _, id := range []string{"widget-W1", "widget-W2", "widget-W3", "widget-W4"} {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id})
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", Text: "chose sqlite", LogType: "decision"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkHandoff, ItemID: "widget-W1", Handoff: ledger.Handoff{Remaining: []string{"the migration"}}})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkBlock, ItemID: "widget-W2", Reason: "waiting on the schema review"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W3"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReject, ItemID: "widget-W3", SessionID: "reviewer", Reason: "no tests for the empty query"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W4"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W5", SessionID: "other"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W5", SessionID: "other"})

	status := board(t, s).SessionStatus("s1")
	for name, got := range map[string]string{
		"holding": statusIDs(status.Holding), "rejected": statusIDs(status.Rejected), "waiting": statusIDs(status.Waiting),
		"inReview": statusIDs(status.InReview), "reviewable": statusIDs(status.Reviewable), "ready": statusIDs(status.Ready),
	} {
		want := map[string]string{"holding": "widget-W1", "rejected": "widget-W3", "waiting": "widget-W2", "inReview": "widget-W4", "reviewable": "widget-W5", "ready": "widget-W6"}[name]
		if got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if r := status.Rejected[0]; r.RejectedBy != "reviewer" || r.Rejection != "no tests for the empty query" {
		t.Fatalf("rejected row = %+v", r)
	}
	if status.Holding[0].LastLog == nil || status.Holding[0].LastLog.Text != "chose sqlite" || status.Waiting[0].Reason != "waiting on the schema review" {
		t.Fatalf("holding %+v waiting %+v", status.Holding[0], status.Waiting[0])
	}
	status.Holding[0].Handoff.Remaining[0] = "changed by a reader"
	if item, _ := board(t, s).WorkItem("widget-W1"); item.Handoff == nil || item.Handoff.Remaining[0] != "the migration" {
		t.Fatalf("a status row shares the board's handoff: %+v", item.Handoff)
	}

	held := board(t, s).HeldBy("s1")
	note := ledger.HeldNote(held, false)
	if !strings.Contains(note, "You hold widget-W3 (endpoint). reviewer sent it back from review: no tests for the empty query. Fix that and submit it again.") {
		t.Fatalf("the start note does not carry the rejection:\n%s", note)
	}
	if strings.Count(note, "sent it back") != 1 {
		t.Fatalf("only the rejected item carries a rejection:\n%s", note)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W3"})
	if status := board(t, s).SessionStatus("s1"); len(status.Rejected) != 0 || statusIDs(status.InReview) != "widget-W3,widget-W4" {
		t.Fatalf("after resubmitting: rejected %v, in review %v", statusIDs(status.Rejected), statusIDs(status.InReview))
	}
}

func TestAHeldParentListsItsUnfinishedChildrenAndWhoHoldsThem(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "search")
	for _, text := range []string{"schema", "index", "endpoint"} {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: text, Fields: ledger.WorkFields{Parent: "widget-W1"}})
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W3", SessionID: "other"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W4", Reason: "obsolete"})

	status := board(t, s).SessionStatus("s1")
	if statusIDs(status.Holding) != "widget-W1" {
		t.Fatalf("holding %q, want the parent", statusIDs(status.Holding))
	}
	row := status.Holding[0]
	want := []ledger.StatusChild{{ID: "widget-W2", Text: "schema", State: ledger.WorkOpen}, {ID: "widget-W3", Text: "index", State: ledger.WorkInProgress, Assignee: "other"}}
	if row.Children == nil || !slices.Equal(row.Children.Unfinished, want) || row.Children.Finished != 1 {
		t.Fatalf("children %+v; want %+v and 1 finished", row.Children, want)
	}

	work(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W4"})
	encoded, err := json.Marshal(board(t, s).SessionStatus("s1").Holding[0])
	if err != nil || !strings.Contains(string(encoded), `"finished":0`) {
		t.Fatalf("a parent with no finished child must still say so: %s %v", encoded, err)
	}
	if rows, _ := json.Marshal(board(t, s).SessionStatus("other").Holding[0]); strings.Contains(string(rows), `"children"`) {
		t.Fatalf("an item with no children carries a children entry: %s", rows)
	}
}
