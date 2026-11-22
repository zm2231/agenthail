package daemon

import (
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func boardPR(t *testing.T, r *registry.Registry, sessionID string, number int, checks string) string {
	t.Helper()
	url := "https://github.com/acme/widget/pull/" + strconv.Itoa(number)
	if _, err := r.LinkPR(registry.SessionPR{SessionID: sessionID, Repo: "acme/widget", Number: number, URL: url, Source: "test", Watch: true}, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	linked, ok, err := r.SessionPR(sessionID, "acme/widget", number)
	if err != nil || !ok {
		t.Fatalf("the linked PR %d reads %v %v", number, ok, err)
	}
	read := linked
	read.Title, read.State, read.Checks, read.Review = "PR "+strconv.Itoa(number), "open", checks, "none"
	if recorded, err := r.RecordPRRead(read, linked.Wakes, time.Now()); err != nil || !recorded {
		t.Fatalf("recording a read of PR %d: %v %v", number, recorded, err)
	}
	return url
}

func TestTheWorkBoardPutsEachHeldItemInTheFirstLaneThatMatches(t *testing.T) {
	d, p, _, base := ledgerPageDaemon(t)
	r := d.Registry
	for _, id := range []string{"s-idle", "s-busy", "s-asking", "s-ended"} {
		p.session(t, r, id)
	}
	busy := surface.Session{ID: "s-busy", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: p.dir, Status: surface.StatusBusy}
	if err := r.RecordObservedSession(busy); err != nil {
		t.Fatal(err)
	}
	ended := surface.Session{ID: "s-ended", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: p.dir, Status: surface.StatusIdle, Ended: true}
	if err := r.RecordObservedSession(ended); err != nil {
		t.Fatal(err)
	}
	store := d.ledgerStore()
	held := func(session, title string) ledger.WorkItem {
		item := work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: session, Action: ledger.WorkAdd, Text: title})
		work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: session, Action: ledger.WorkClaim, ItemID: item.ID})
		return item
	}
	logged := func(item ledger.WorkItem, session, text string) {
		work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: session, Action: ledger.WorkLog, ItemID: item.ID, Text: text})
	}

	failingURL := boardPR(t, r, "s-idle", 7, "failing")
	pendingURL := boardPR(t, r, "s-idle", 8, "pending")
	mergedURL := boardPR(t, r, "s-idle", 9, "failing")
	boardPR(t, r, "s-idle", 10, "failing")

	failing := held("s-idle", "Fix the flaky index test")
	logged(failing, "s-idle", "PR opened: "+failingURL)
	pending := held("s-idle", "Add the date filter")
	logged(pending, "s-idle", "PR opened: "+pendingURL)
	merged := held("s-idle", "Ship the search box")
	logged(merged, "s-idle", "PR opened: "+mergedURL)
	logged(merged, "s-idle", "PR merged: "+mergedURL)
	idle := held("s-idle", "Write the search docs")
	logged(idle, "s-idle", "outline drafted\nsections next")
	running := held("s-busy", "Build the order index")
	asking := held("s-asking", "Keep deleted orders searchable")
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s-asking", Action: ledger.WorkSubmit, ItemID: asking.ID})
	if _, err := r.RaiseAsk(registry.Ask{SessionID: "s-asking", Title: "Keep deleted orders 30 days?", RoutedTo: registry.AskToPerson}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reviewed := held("s-ended", "Drop the old search")
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s-ended", Action: ledger.WorkSubmit, ItemID: reviewed.ID})
	waiting := held("s-busy", "Move search to the new host")
	work(t, store, ledger.WorkRequest{ProjectKey: p.key, SessionID: "s-busy", Action: ledger.WorkDep, ItemID: waiting.ID, DependsOn: running.ID})

	_, view := ledgerPageRead(t, base, "proj-widget", "test-token", "")
	lanes := map[string]string{}
	for lane, rows := range view.Board {
		for _, row := range rows {
			lanes[row.ID] = lane
		}
	}
	for item, want := range map[string]string{
		asking.ID:   laneYou,
		failing.ID:  laneCIFailing,
		pending.ID:  laneCIPending,
		reviewed.ID: laneReview,
		running.ID:  laneRunning,
		waiting.ID:  laneBlocked,
		merged.ID:   laneIdle,
		idle.ID:     laneIdle,
	} {
		if lanes[item] != want {
			t.Errorf("%s is in lane %q, want %q", item, lanes[item], want)
		}
	}
	if len(lanes) != 8 {
		t.Errorf("the board holds %d items, want each held item once: %v", len(lanes), lanes)
	}

	flights := map[string]*ledgerPageFlight{}
	for _, rows := range view.Board {
		for _, row := range rows {
			flights[row.ID] = row.Flight
		}
	}
	if prs := flights[failing.ID].PRs; len(prs) != 1 || prs[0].URL != failingURL || prs[0].Number != 7 || prs[0].Checks != "failing" || prs[0].State != "open" {
		t.Errorf("the failing item's PRs read %+v", prs)
	}
	if prs := flights[merged.ID].PRs; len(prs) != 1 || prs[0].State != "merged" {
		t.Errorf("a merged PR reads %+v", prs)
	}
	if prs := flights[idle.ID].PRs; len(prs) != 0 {
		t.Errorf("an item whose log names no PR shows %+v from its holder's other PRs", prs)
	}
	if last := flights[idle.ID].LastLog; last == nil || last.Text != "outline drafted" {
		t.Errorf("the newest log line reads %+v", last)
	}
	if asks := flights[asking.ID].Asks; len(asks) != 1 || asks[0].Title != "Keep deleted orders 30 days?" {
		t.Errorf("the asking item's asks read %+v", asks)
	}
	for item, want := range map[string]string{running.ID: holderWorking, idle.ID: holderIdle, reviewed.ID: holderEnded} {
		if got := flights[item].HolderState; got != want {
			t.Errorf("%s's holder is %q, want %q", item, got, want)
		}
	}
	if waits := flights[waiting.ID].WaitsOn; len(waits) != 1 || waits[0] != running.ID {
		t.Errorf("the waiting item waits on %v", waits)
	}
}
