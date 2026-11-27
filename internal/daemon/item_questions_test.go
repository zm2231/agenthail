package daemon

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f *mcpAnswerFixture) hold(t *testing.T, sessionID, text string, blocked bool) (string, string) {
	t.Helper()
	store, key := f.d.ledgerStore(), ledgerKeyAt(t, f.r, f.surface.sessions[sessionID].Cwd)
	added, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: sessionID, Action: ledger.WorkAdd, Text: text}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	steps := []ledger.WorkRequest{{Action: ledger.WorkClaim}}
	if blocked {
		steps = append(steps, ledger.WorkRequest{Action: ledger.WorkBlock, Reason: "waits on the export"})
	}
	for _, step := range steps {
		step.ProjectKey, step.SessionID, step.ItemID = key, sessionID, added.Item.ID
		if _, err := store.Work(step, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return key, added.Item.ID
}

func (f *mcpAnswerFixture) syncQuestions(t *testing.T) {
	t.Helper()
	f.d.background.wait()
	f.d.syncItemQuestions(t.Context())
}

func (f *mcpAnswerFixture) itemQuestions(t *testing.T, key, id string) []ledger.ItemQuestion {
	t.Helper()
	board, err := f.d.ledgerStore().Board(key)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := board.WorkItem(id)
	return item.Questions
}

func (f *mcpAnswerFixture) lanes(t *testing.T) (map[string]string, map[string]*ledgerPageFlight) {
	t.Helper()
	view, err := f.d.projectLedgerPage("p-answer")
	if err != nil {
		t.Fatal(err)
	}
	lanes, flights := map[string]string{}, map[string]*ledgerPageFlight{}
	for lane, rows := range view.Board {
		for _, row := range rows {
			lanes[row.ID], flights[row.ID] = lane, row.Flight
		}
	}
	return lanes, flights
}

func TestAnAskFromASessionHoldingWorkIsAQuestionOnTheItemItIsAbout(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, asked := f.hold(t, "lead", "Import the March export", false)
	_, other := f.hold(t, "lead", "Rename the export columns", true)

	out, refused := callMCP(t, f.lead, "agenthail_ask", deployAsk)
	if refused || out["workItem"] != asked {
		t.Fatalf("an ask from a session with one item in progress is not linked to it: %v", out)
	}
	askID, _ := out["askId"].(string)
	f.syncQuestions(t)
	questions := f.itemQuestions(t, key, asked)
	if len(questions) != 1 || !questions[0].Open() || questions[0].AskID != askID || questions[0].Title != "Deploy the fix?" ||
		len(questions[0].Prompts) != 1 || questions[0].Prompts[0].Options[1].Does != "hold until Monday" || *questions[0].Prompts[0].Recommended != 0 {
		t.Fatalf("the item's question reads %+v", questions)
	}
	if again := f.itemQuestions(t, key, other); len(again) != 0 {
		t.Fatalf("the item the ask is not about got %+v", again)
	}
	f.syncQuestions(t)
	if questions := f.itemQuestions(t, key, asked); len(questions) != 1 {
		t.Fatalf("a second sync recorded the ask again: %+v", questions)
	}

	lanes, flights := f.lanes(t)
	if lanes[asked] != laneYou || !slices.Contains(flights[asked].WaitsOn, "person") || lanes[other] != laneBlocked {
		t.Fatalf("lanes = %v; the asked item waits on %v", lanes, flights[asked].WaitsOn)
	}
	page := freshDecisions(t, f.d)
	if i := slices.IndexFunc(page.Decisions, func(item decisionItem) bool { return item.Key == "ask:"+askID }); i < 0 ||
		page.Decisions[i].WorkItem != asked || page.Decisions[i].LedgerKey != key || page.Decisions[i].WorkTitle != "Import the March export" {
		t.Fatalf("Needs you does not name the ask's item: %+v", page.Decisions)
	}

	person := principal{ID: localDashboardPrincipal, Kind: principalPerson}
	if reply := f.d.doAction(t.Context(), person, dashboardActionRequest{Action: "respond", SessionID: "lead", RequestID: askID, Decision: surface.Response{Choices: []string{"o1"}}}); reply.Status != 200 {
		t.Fatalf("answer status=%d %s", reply.Status, reply.Body)
	}
	f.syncQuestions(t)
	if questions := f.itemQuestions(t, key, asked); len(questions) != 1 || questions[0].Open() || questions[0].Outcome != ledger.ItemQuestionAnswered || !strings.Contains(questions[0].Answer, "Deploy") {
		t.Fatalf("after the answer the item's question reads %+v", questions)
	}
	if lanes, _ := f.lanes(t); lanes[asked] == laneYou {
		t.Fatal("an answered question kept its item waiting on the person")
	}
}

func TestAnAskNamesItsItemWhenTheSessionHoldsSeveralAndAPlainAskMarksThemAll(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, first := f.hold(t, "lead", "Import the March export", false)
	_, second := f.hold(t, "lead", "Rename the export columns", false)

	out, refused := callMCP(t, f.lead, "agenthail_ask", deployAsk)
	if refused || out["workItem"] != nil || !strings.Contains(out["note"].(string), "2 items") {
		t.Fatalf("an ask from a session with two items in progress = %v", out)
	}
	f.syncQuestions(t)
	if lanes, _ := f.lanes(t); lanes[first] != laneYou || lanes[second] != laneYou {
		t.Fatalf("a plain ask does not mark every item its session holds: %v", lanes)
	}
	if reply := f.d.doAction(t.Context(), principal{ID: localDashboardPrincipal, Kind: principalPerson}, dashboardActionRequest{Action: "respond", SessionID: "lead", RequestID: out["askId"].(string), Decision: surface.Response{Choices: []string{"o1"}}}); reply.Status != 200 {
		t.Fatalf("answer status=%d %s", reply.Status, reply.Body)
	}

	named := map[string]any{"title": deployAsk["title"], "questions": deployAsk["questions"], "item": second}
	if out, refused := callMCP(t, f.lead, "agenthail_ask", named); refused || out["workItem"] != second {
		t.Fatalf("an ask naming its item = %v", out)
	}
	f.syncQuestions(t)
	if len(f.itemQuestions(t, key, second)) != 1 || len(f.itemQuestions(t, key, first)) != 0 {
		t.Fatal("the named item does not carry the ask alone")
	}
	if lanes, _ := f.lanes(t); lanes[second] != laneYou || lanes[first] == laneYou {
		t.Fatalf("a linked ask marks more than its item: %v", lanes)
	}

	elsewhere := map[string]any{"title": deployAsk["title"], "questions": deployAsk["questions"], "item": "app-W99"}
	if out, refused := callMCP(t, f.lead, "agenthail_ask", elsewhere); !refused || !strings.Contains(out["message"].(string), "not held by this session") {
		t.Fatalf("an ask naming an item the session does not hold = %v", out)
	}
}

func TestAWorkersAskWithItsLeadDoesNotMarkTheItemAsWaitingOnThePerson(t *testing.T) {
	f := newMCPAnswerFixture(t)
	_, asked := f.hold(t, "worker", "Import the March export", false)
	out, refused := callMCP(t, f.worker, "agenthail_ask", deployAsk)
	if refused || out["workItem"] != asked || out["routedTo"] != "lead" {
		t.Fatalf("a worker's ask = %v", out)
	}
	f.syncQuestions(t)
	if lanes, flights := f.lanes(t); lanes[asked] == laneYou || slices.Contains(flights[asked].WaitsOn, "person") {
		t.Fatalf("an ask its lead holds reads as the person's: %v", lanes)
	}
}

func TestAnAskKeepsItsItemWaitingOnThePersonAfterAHandoffBeforeItsQuestionIsRecorded(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, asked := f.hold(t, "lead", "Import the March export", false)
	// The ask queues a background sync that records its question the moment this lock is released.
	f.d.itemQuestionsMu.Lock()
	unlock := sync.OnceFunc(f.d.itemQuestionsMu.Unlock)
	t.Cleanup(unlock)
	out, refused := callMCP(t, f.lead, "agenthail_ask", deployAsk)
	if refused || out["workItem"] != asked {
		t.Fatalf("ask = %v", out)
	}
	store := f.d.ledgerStore()
	for _, step := range []ledger.WorkRequest{
		{Action: ledger.WorkHandoff, SessionID: "lead", Handoff: ledger.Handoff{Remaining: []string{"deploy once the person answers"}}},
		{Action: ledger.WorkClaim, SessionID: "worker"},
	} {
		step.ProjectKey, step.ItemID = key, asked
		if _, err := store.Work(step, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.itemQuestions(t, key, asked)) != 0 {
		t.Fatal("the question was recorded before the handoff; the test does not reach the unsynced ask")
	}
	lanes, flights := f.lanes(t)
	unlock()
	if lanes[asked] != laneYou || !slices.Contains(flights[asked].WaitsOn, "person") {
		t.Fatalf("the handed-off item stopped waiting on the person before its question was recorded: %v", lanes)
	}
	f.syncQuestions(t)
	if lanes, _ := f.lanes(t); len(f.itemQuestions(t, key, asked)) != 1 || lanes[asked] != laneYou {
		t.Fatalf("after the question is recorded: lanes %v, questions %+v", lanes, f.itemQuestions(t, key, asked))
	}
}

func TestAnAskOnAnItemDeletedBeforeItsQuestionIsRecordedStaysAPlainAsk(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, asked := f.hold(t, "lead", "Import the March export", false)
	f.d.itemQuestionsMu.Lock()
	out, refused := callMCP(t, f.lead, "agenthail_ask", deployAsk)
	_, err := f.d.ledgerStore().Work(ledger.WorkRequest{ProjectKey: key, SessionID: "lead", Action: ledger.WorkDelete, ItemID: asked}, time.Now())
	f.d.itemQuestionsMu.Unlock()
	if refused || out["workItem"] != asked || err != nil {
		t.Fatalf("ask = %v, delete: %v", out, err)
	}
	for range 3 {
		f.syncQuestions(t)
	}
	pending, err := f.r.PendingItemQuestions()
	if err != nil || len(pending) != 0 {
		t.Fatalf("an ask on a deleted item is still pending: %+v %v", pending, err)
	}
	if ask, _, _ := f.r.Ask(out["askId"].(string)); ask.State != registry.AskOpen || ask.LedgerQuestion != "" {
		t.Fatalf("the ask reads %+v", ask)
	}
}

func TestAnAskOnWorkInALedgerAPairedHostKeepsIsRecordedThereOnceThroughOutagesAndLostReplies(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	var added ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "add the index"}, &added)
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkClaim, ItemID: added.Item.ID}, nil)
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	questions := func() int { return countEvents(t, owner.reg, ledger.EventWorkQuestion, "") }
	stored := func(id string) string {
		t.Helper()
		ask, found, err := mac.reg.Ask(id)
		if err != nil || !found {
			t.Fatalf("read %s: %v %v", id, found, err)
		}
		return ask.LedgerQuestion
	}

	owner.gate.proxyDown.Store(true)
	session, err := mac.reg.Session("local-a")
	if err != nil {
		t.Fatal(err)
	}
	ask, _, err := mac.daemon.raiseAsk(*session, askInput{Title: "Index on created_at?", Questions: []askQuestionInput{{Question: "Which column?", Options: []askOptionInput{{Label: "created_at"}, {Label: "updated_at"}}}}})
	if err != nil || ask.WorkItem != added.Item.ID || ask.LedgerKey != widgetKey {
		t.Fatalf("an ask while the ledger's owner is down = %+v, %v", ask, err)
	}
	mac.daemon.background.wait()
	mac.daemon.syncItemQuestions(t.Context())
	if n := questions(); n != 0 || stored(ask.ID) != "" {
		t.Fatalf("with the owner down: %d questions, stored %q", n, stored(ask.ID))
	}

	owner.gate.proxyDown.Store(false)
	owner.gate.ledgerDrops.Store(true)
	owner.gate.drops.Store(50)
	mac.daemon.syncItemQuestions(t.Context())
	if n := questions(); n != 1 || stored(ask.ID) != "" || owner.gate.drops.Load() > 48 {
		t.Fatalf("with every reply lost: %d questions on the owner, stored %q, %d drops left", n, stored(ask.ID), owner.gate.drops.Load())
	}
	owner.gate.drops.Store(0)
	mac.daemon.syncItemQuestions(t.Context())
	if n := questions(); n != 1 || stored(ask.ID) == "" {
		t.Fatalf("once replies get through: %d questions on the owner, stored %q", n, stored(ask.ID))
	}

	if err := mac.daemon.withdrawOwnAsk(*session, ask.ID); err != nil {
		t.Fatal(err)
	}
	mac.daemon.background.wait()
	for range 2 {
		mac.daemon.syncItemQuestions(t.Context())
	}
	if n := countEvents(t, owner.reg, ledger.EventWorkQuestionRes, ""); n != 1 {
		t.Fatalf("the withdrawn ask resolved its question %d times", n)
	}
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	board, err := mac.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if item, _ := board.WorkItem(added.Item.ID); len(item.Questions) != 1 || item.Questions[0].Outcome != ledger.ItemQuestionWithdrawn {
		t.Fatalf("the replica's item reads %+v", item.Questions)
	}

	var gone ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "drop the old index"}, &gone)
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkClaim, ItemID: gone.Item.ID}, nil)
	mac.daemon.itemQuestionsMu.Lock()
	late, _, err := mac.daemon.raiseAsk(*session, askInput{Title: "Drop it now?", Item: gone.Item.ID, Questions: []askQuestionInput{{Question: "Now?", Options: []askOptionInput{{Label: "yes"}, {Label: "no"}}}}})
	if err == nil {
		mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkDelete, ItemID: gone.Item.ID}, nil)
	}
	mac.daemon.itemQuestionsMu.Unlock()
	if err != nil || late.WorkItem != gone.Item.ID {
		t.Fatalf("an ask on the second item = %+v, %v", late, err)
	}
	mac.daemon.background.wait()
	for range 3 {
		mac.daemon.syncItemQuestions(t.Context())
	}
	if pending, err := mac.reg.PendingItemQuestions(); err != nil || len(pending) != 0 || questions() != 1 {
		t.Fatalf("an ask on an item its owner deleted: pending %+v %v, %d questions", pending, err, questions())
	}
}
