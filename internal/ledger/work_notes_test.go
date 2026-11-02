package ledger_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func noted(t *testing.T, s *ledger.Store) {
	t.Helper()
	searchAsk(t, s)
	items(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "reviewer", Text: "check the migration too"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", Text: "index built; migration next", LogType: "result"})
}

func entryKinds(item ledger.WorkItem) string {
	var kinds []string
	for _, e := range item.Timeline {
		kind := e.Kind
		if e.Kind == "state" {
			kind += ":" + string(e.State)
		}
		kinds = append(kinds, kind)
	}
	return strings.Join(kinds, " ")
}

func bothBoards(t *testing.T, db *sql.DB, s *ledger.Store) map[string]*ledger.Board {
	t.Helper()
	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	cached := board(t, s)
	if got, want := snapshot(t, cached), snapshot(t, rebuilt); got != want {
		t.Fatalf("cached board differs from the rebuilt one:\n got %s\nwant %s", got, want)
	}
	return map[string]*ledger.Board{"cached": cached, "rebuilt": rebuilt}
}

func TestCommentsAndLogsJoinTheItemTimeline(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	noted(t, s)
	items(t, s, "wire the endpoint")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W1", DependsOn: "widget-W2"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1"})
	capture(t, s, project, "claude-prompt:2", "approve it")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:2"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "reviewer", Text: "approved after the migration check"})

	for name, b := range bothBoards(t, db, s) {
		item, ok := b.WorkItem("widget-W1")
		if !ok {
			t.Fatalf("%s board: no widget-W1", name)
		}
		if got, want := entryKinds(item), "created state:in_progress comment log dep state:in_review state:done comment"; got != want {
			t.Fatalf("%s board timeline = %q, want %q", name, got, want)
		}
		comment, log := item.Timeline[2], item.Timeline[3]
		if comment.Actor != "reviewer" || comment.Text != "check the migration too" || comment.At.IsZero() {
			t.Fatalf("%s board comment entry = %+v", name, comment)
		}
		if log.Actor != "s1" || log.LogType != "result" || log.Text != "index built; migration next" {
			t.Fatalf("%s board log entry = %+v", name, log)
		}
		if done := item.Timeline[6]; !strings.Contains(done.Text, "reviewed by person") {
			t.Fatalf("%s board approval entry = %+v", name, done)
		}
	}
}

func TestOnlyTheAssigneeLogsAndNotesNeedText(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", Text: "started"}, "has no assignee")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "reviewer", Text: "anyone may comment"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", SessionID: "reviewer", Text: "not mine"}, "only its assignee logs")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", Text: "x", LogType: "orchestration"}, "unknown log type")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", Text: "   "}, "needs its text")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", Text: strings.Repeat("é", 4001)}, "at most 4000 characters")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W9", Text: "hello"}, "not a work item")
	if got := work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", Text: strings.Repeat("é", 4000)}).Readback; got != "intent ledger: progress log on widget-W1" {
		t.Fatalf("readback = %q", got)
	}
}

func TestNotesSurviveACutAndTheReplayARetractionTriggers(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	capture(t, s, project, "claude-prompt:9", "unrelated words")
	noted(t, s)
	cut, err := json.Marshal(ledger.CutPayload{CutID: "widget-X1", ClauseID: "widget-C1", Reason: "narrowed", Acked: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent(ledger.Event{ProjectKey: project, Type: ledger.EventCut, Payload: cut, ActorSessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if done, err := s.ReclassifyUtterance(project, "claude-prompt:9", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	for name, b := range bothBoards(t, db, s) {
		item, ok := b.WorkItem("widget-W1")
		if !ok || entryKinds(item) != "created state:in_progress comment log" {
			t.Fatalf("%s board lost the notes after a cut and a replay: %+v", name, item)
		}
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "reviewer", Text: "still worth finishing"})
	bothBoards(t, db, s)
}

func TestNotesGoWithAnItemWhoseWordsAreRetracted(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	noted(t, s)
	if done, err := s.ReclassifyUtterance(project, "claude-prompt:1", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	for name, b := range bothBoards(t, db, s) {
		if _, ok := b.WorkItem("widget-W1"); ok {
			t.Fatalf("%s board keeps an item resting on retracted words", name)
		}
		found := false
		for _, r := range b.Retractions() {
			found = found || r.ID == "widget-W1"
		}
		if !found {
			t.Fatalf("%s board: no retraction notice for widget-W1: %+v", name, b.Retractions())
		}
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", Text: "too late"}, "not a work item")
}

func TestReturnedWorkItemsDoNotShareBoardStorage(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	result := work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W2", Text: "after the schema"})
	result.Item.Timeline[0].Text, result.Item.DependsOn[0] = "changed", "widget-W9"
	path := board(t, s).CriticalPath()
	path[len(path)-1].Timeline[0].Text = "changed"
	item, _ := board(t, s).WorkItem("widget-W2")
	if item.Timeline[0].Text != "index" || item.DependsOn[0] != "widget-W1" {
		t.Fatalf("a caller's edit reached the board: %+v", item)
	}
}

func TestReadyPutsWhatUnblocksMostFirst(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "schema", "index", "endpoint", "docs", "release")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W2", DependsOn: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W3", DependsOn: "widget-W2"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: "widget-W5", DependsOn: "widget-W4"})
	ids := func() string {
		var out []string
		for _, item := range board(t, s).Ready() {
			out = append(out, item.ID)
		}
		return strings.Join(out, " ")
	}
	if got := ids(); got != "widget-W1 widget-W4" {
		t.Fatalf("ready = %q", got)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	if got := ids(); got != "widget-W4" {
		t.Fatalf("ready after claiming W1 = %q", got)
	}
}
