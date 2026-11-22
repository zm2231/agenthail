package daemon

import (
	"context"
	"strconv"
	"sync/atomic"
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
	forged := held("s-idle", "Tidy the index names")
	logged(forged, "s-idle", "PR opened: javascript:alert(1)")
	logged(forged, "s-idle", "PR opened: https://github.com/acme/widget/pull/99")
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
		forged.ID:   laneIdle,
	} {
		if lanes[item] != want {
			t.Errorf("%s is in lane %q, want %q", item, lanes[item], want)
		}
	}
	if len(lanes) != 9 {
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
	if prs := flights[forged.ID].PRs; len(prs) != 0 {
		t.Errorf("log lines naming PRs this host never linked show %+v", prs)
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

func TestAnOpenWorkPageHearsWhenItsHoldersOrPRsChangeButNotOtherPRs(t *testing.T) {
	shortLedgerChangeWait(t)
	const wait = 50 * time.Millisecond
	d, p, _, base := ledgerPageDaemon(t)
	d.ledgerBoards.wait = wait
	r := d.Registry
	fake := d.Surfaces[0].(*daemonSurface)
	holder := surface.Session{ID: "s-idle", Surface: fake.Name(), Cwd: p.dir, Status: surface.StatusIdle, LastActive: time.Now()}
	fake.sessions[holder.ID] = holder
	ctx := context.Background()
	d.discoverCatalog(ctx)
	key, err := d.ledgerPageTarget("proj-widget")
	if err != nil || key == "" {
		t.Fatalf("the page reads the ledger of %q, %v", key, err)
	}
	boardPR(t, r, "s-idle", 8, "pending")
	store := d.ledgerStore()
	item := work(t, store, ledger.WorkRequest{ProjectKey: key, SessionID: "s-idle", Action: ledger.WorkAdd, Text: "Fix the flaky index test"})
	work(t, store, ledger.WorkRequest{ProjectKey: key, SessionID: "s-idle", Action: ledger.WorkClaim, ItemID: item.ID})
	shown := "https://github.com/acme/widget/pull/7"
	if err := d.prs.Link(registry.SessionPR{SessionID: "s-idle", Repo: "acme/widget", Number: 7, URL: shown, Source: "test", Watch: true}, true); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(ctx)
	events := openWorkPageEvents(t, base, "proj-widget")
	drain := func() {
		for {
			select {
			case <-events:
			case <-time.After(5 * ledgerChangeWait):
				return
			}
		}
	}
	drain()
	reread := func(number int, checks string) {
		t.Helper()
		row, _, err := r.SessionPR("s-idle", "acme/widget", number)
		if err != nil {
			t.Fatal(err)
		}
		next := row
		next.Checks = checks
		if _, err := d.changePRs(func(r *registry.Registry) (bool, error) { return r.RecordPRRead(next, row.Wakes, time.Now()) }); err != nil {
			t.Fatal(err)
		}
		d.discoverCatalog(ctx)
	}

	reread(8, "failing")
	select {
	case change := <-events:
		t.Fatalf("the page heard %+v for a PR no item on it names", change)
	case <-time.After(5 * wait):
	}
	heard := func(what, lane string) {
		t.Helper()
		select {
		case change := <-events:
			if change.ProjectKey != key || change.Version == "" {
				t.Fatalf("after %s the page heard %+v", what, change)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the page heard nothing after %s", what)
		}
		if _, view := ledgerPageRead(t, base, "proj-widget", "test-token", ""); len(view.Board[lane]) != 1 {
			t.Fatalf("after %s the board reads %+v", what, view.Board)
		}
	}
	reread(7, "failing")
	heard("the checks of a PR it shows failed", laneCIFailing)
	reread(7, "passing")
	heard("the checks passed", laneIdle)
	holder.Status = surface.StatusBusy
	fake.sessions[holder.ID] = holder
	d.discoverCatalog(ctx)
	heard("its holder went busy", laneRunning)
}

func TestManyOpenWorkPagesOfOneProjectShareOneBoardReadPerWait(t *testing.T) {
	const wait = 100 * time.Millisecond
	d, p, _, base := ledgerPageDaemon(t)
	d.ledgerBoards.wait = wait
	r := d.Registry
	fake := d.Surfaces[0].(*daemonSurface)
	fake.sessions["s-idle"] = surface.Session{ID: "s-idle", Surface: fake.Name(), Cwd: p.dir, Status: surface.StatusIdle, LastActive: time.Now()}
	ctx := context.Background()
	d.discoverCatalog(ctx)
	boardPR(t, r, "s-idle", 8, "pending")
	d.discoverCatalog(ctx)
	var reads atomic.Int32
	d.ledgerBoards.read = func(projectID string) string {
		reads.Add(1)
		return d.ledgerBoardVersion(projectID)
	}
	const pages = 8
	for range pages {
		openWorkPageEvents(t, base, "proj-widget")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.ledgerBoards.mu.Lock()
		open := len(d.ledgerBoards.pages["proj-widget"])
		d.ledgerBoards.mu.Unlock()
		if open == pages {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d pages are watching", open, pages)
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	checks := []string{"failing", "pending"}
	for i := range 12 {
		row, _, err := r.SessionPR("s-idle", "acme/widget", 8)
		if err != nil {
			t.Fatal(err)
		}
		next := row
		next.Checks = checks[i%2]
		if _, err := d.changePRs(func(r *registry.Registry) (bool, error) { return r.RecordPRRead(next, row.Wakes, time.Now()) }); err != nil {
			t.Fatal(err)
		}
		d.discoverCatalog(ctx)
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(2 * wait)
	elapsed := time.Since(start)
	if got, most := int(reads.Load()), 1+int(elapsed/wait)+2; got < 2 || got > most {
		t.Fatalf("%d pages read the board %d times in %s, want 2 to %d", pages, got, elapsed, most)
	}
}

func TestAWorkPageHearsABoardChangeThatLandsWhileItsWatchStarts(t *testing.T) {
	d, p, _, base := ledgerPageDaemon(t)
	d.ledgerBoards.wait = 50 * time.Millisecond
	r := d.Registry
	fake := d.Surfaces[0].(*daemonSurface)
	fake.sessions["s-idle"] = surface.Session{ID: "s-idle", Surface: fake.Name(), Cwd: p.dir, Status: surface.StatusIdle, LastActive: time.Now()}
	ctx := context.Background()
	d.discoverCatalog(ctx)
	key, err := d.ledgerPageTarget("proj-widget")
	if err != nil || key == "" {
		t.Fatalf("the page reads the ledger of %q, %v", key, err)
	}
	store := d.ledgerStore()
	item := work(t, store, ledger.WorkRequest{ProjectKey: key, SessionID: "s-idle", Action: ledger.WorkAdd, Text: "Fix the flaky index test"})
	work(t, store, ledger.WorkRequest{ProjectKey: key, SessionID: "s-idle", Action: ledger.WorkClaim, ItemID: item.ID})
	if err := d.prs.Link(registry.SessionPR{SessionID: "s-idle", Repo: "acme/widget", Number: 7, URL: "https://github.com/acme/widget/pull/7", Source: "test", Watch: true}, true); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(ctx)

	var baseline atomic.Bool
	d.ledgerBoards.read = func(projectID string) string {
		version := d.ledgerBoardVersion(projectID)
		if baseline.CompareAndSwap(false, true) {
			row, _, err := r.SessionPR("s-idle", "acme/widget", 7)
			if err != nil {
				t.Error(err)
				return version
			}
			next := row
			next.State, next.Checks = "open", "failing"
			if _, err := d.changePRs(func(r *registry.Registry) (bool, error) { return r.RecordPRRead(next, row.Wakes, time.Now()) }); err != nil {
				t.Error(err)
			}
			d.discoverCatalog(ctx)
		}
		return version
	}
	events := openWorkPageEvents(t, base, "proj-widget")
	select {
	case change := <-events:
		if change.Version == "" {
			t.Fatalf("the page heard %+v", change)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a board change made while the page's watch started was never heard")
	}
	if _, view := ledgerPageRead(t, base, "proj-widget", "test-token", ""); len(view.Board[laneCIFailing]) != 1 {
		t.Fatalf("the board reads %+v", view.Board)
	}
}
