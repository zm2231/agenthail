package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func freshDecisions(t *testing.T, d *Daemon) decisionsPage {
	t.Helper()
	d.discoverCatalog(t.Context())
	page, response := readDecisions(t, d, "")
	if response.Code != http.StatusOK {
		t.Fatalf("decisions answered %d %s", response.Code, response.Body)
	}
	return page
}

func waitingTurns(page decisionsPage) []decisionItem {
	var turns []decisionItem
	for _, item := range page.Decisions {
		if item.Kind == decisionTurn {
			turns = append(turns, item)
		}
	}
	return turns
}

func TestATurnThatEndsWaitingOnThePersonIsInNeedsYouAndPushesOnceUntilTheNextTurn(t *testing.T) {
	f := newAskFixture(t)
	pushes := newPushRecorder(t, f.r)
	f.turn(t, f.from, "turn-1", func() {
		if out, refused := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": "The import is ready", "waitingOn": "Upload the March export to the shared folder"}); refused {
			t.Fatalf("end turn refused: %v", out)
		}
	})
	f.d.background.wait()

	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("a turn waiting on the person pushed needs-you %d times", count)
	}
	if push := pushes.first(turnNeedsYouEvent); push["message"] != "Needs you: Upload the March export to the shared folder" || push["turnId"] != "turn-1" {
		t.Fatalf("needs-you push %+v", push)
	}
	if finished := pushes.all("turn.completed"); len(finished) != 0 {
		t.Fatalf("the waiting turn also pushed finished: %+v", finished)
	}
	row := viewRow(t, f.d, f.from.ID)
	if state := row.TurnState; state == nil || state.State != turnWaitingPerson || state.Source != turnSourceEndTurn || state.Reason != "Upload the March export to the shared folder" || !state.Pushed || state.TurnID != "turn-1" {
		t.Fatalf("the row's turn state = %+v", row.TurnState)
	}
	page := freshDecisions(t, f.d)
	turns := waitingTurns(page)
	if len(turns) != 1 || turns[0].Key != "turn:"+f.from.ID+":turn-1" || turns[0].Group != decisionWaiting || turns[0].Reason != "Upload the March export to the shared folder" || turns[0].Answerable {
		t.Fatalf("decisions = %+v", page.Decisions)
	}

	f.observeTurnRunning(t, f.from, "turn-2")
	if page := freshDecisions(t, f.d); len(waitingTurns(page)) != 0 {
		t.Fatalf("the wait stayed in Needs you once the next turn started: %+v", page.Decisions)
	}
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("the next turn pushed the wait again: %d", count)
	}
}

func (f askFixture) observeTurnRunning(t *testing.T, session surface.Session, id string) {
	t.Helper()
	if err := f.r.OpenTurn(session.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: id}
	f.d.observeSession(context.Background(), f.fake, &session)
}

func TestThePersonDismissesAWaitingTurnAndATypedTurnWaitsWithoutAPush(t *testing.T) {
	f := newAskFixture(t)
	pushes := newPushRecorder(t, f.r)
	submitPrompt(t, f.d, f.from.ID, "typed-1", "get the import ready")
	f.turn(t, f.from, "turn-1", func() {
		callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": "Ready", "waitingOn": "Upload the March export"})
	})
	f.d.background.wait()
	if count := pushes.count(turnNeedsYouEvent); count != 0 {
		t.Fatalf("a turn the person typed pushed needs-you %d times", count)
	}
	page := freshDecisions(t, f.d)
	if turns := waitingTurns(page); len(turns) != 1 {
		t.Fatalf("a typed turn that waits on the person is not in Needs you: %+v", page.Decisions)
	}

	if reply := dismiss(t, f.d, sessionPrincipal(f.to.ID), dashboardActionRequest{SessionID: f.from.ID}); reply.Status != http.StatusForbidden {
		t.Fatalf("an agent dismissed the person's wait: %d %s", reply.Status, reply.Body)
	}
	mustDismiss(t, f.d, dashboardActionRequest{SessionID: f.from.ID})
	if page := freshDecisions(t, f.d); len(waitingTurns(page)) != 0 {
		t.Fatalf("a dismissed wait is still in Needs you: %+v", page.Decisions)
	}

	dismissed := page.Decisions[0]
	later := dismissed
	later.Key = "turn:" + f.from.ID + ":turn-2"
	sources := decisionSources{dismissed: map[string]string{f.from.ID: decisionStateKey(dismissed)}}
	if !sources.dismissedNow(dismissed) || sources.dismissedNow(later) {
		t.Fatal("a dismissal hides a later wait with the same words ended in the same millisecond")
	}
}

func TestAWaitIsOneLineOnWorkTheSessionHolds(t *testing.T) {
	f := newAskFixture(t)
	for _, c := range []struct {
		input map[string]any
		want  string
	}{
		{map[string]any{"headline": "Ready", "waitingOn": strings.Repeat("upload it ", 13)}, "at most 120 characters"},
		{map[string]any{"headline": "Ready", "waitingOn": "Upload it", "item": "widget-W9"}, "not held by this session"},
		{map[string]any{"headline": "Ready", "item": "widget-W9"}, "give waitingOn with it"},
	} {
		out, refused := callMCP(t, f.person, "agenthail_end_turn", c.input)
		if !refused || !strings.Contains(out["message"].(string), c.want) {
			t.Fatalf("end turn %v = %v, want refused naming %q", c.input, out, c.want)
		}
	}
}

func TestAWorkersWaitIsHandledByItsLeadUntilTheLeadIsGone(t *testing.T) {
	f := newMCPAnswerFixture(t)
	leadSession := f.asProjectLead(t, "lead")
	worker := f.surface.sessions["worker"]
	store, key := f.d.ledgerStore(), ledgerKeyAt(t, f.r, worker.Cwd)
	added, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkAdd, Text: "import the March export"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkClaim, ItemID: added.Item.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Ready", "waitingOn": "Upload the March export"})

	page := freshDecisions(t, f.d)
	if turns := waitingTurns(page); len(turns) != 0 || len(page.LeadHandling) != 1 {
		t.Fatalf("with its lead free, the worker's wait went to the person: %+v", page)
	}
	if held := page.LeadHandling[0]; held.SessionID != "worker" || held.HeldBy != "lead" || held.Reason != "Upload the March export" || held.WorkItem != added.Item.ID || held.LedgerKey != key {
		t.Fatalf("handled by the lead: %+v", held)
	}

	lead := f.surface.sessions["lead"]
	for _, observation := range []*surface.TurnObservation{{Status: surface.StatusIdle}, {Status: surface.StatusBusy, ActiveTurnID: "lead-1"}} {
		f.surface.observations["lead"] = observation
		f.d.observeSession(t.Context(), f.surface, &lead)
	}
	if out, refused := callMCP(t, leadSession, "agenthail_end_turn", map[string]any{"headline": "Blocked", "waitingOn": "Sign in to the build service"}); refused {
		t.Fatalf("the lead's end turn refused: %v", out)
	}
	f.surface.observations["lead"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "lead-1", Reply: &surface.ReplyResult{Text: "reply", Done: true}}
	f.d.observeSession(t.Context(), f.surface, &lead)
	f.d.background.wait()
	page = freshDecisions(t, f.d)
	if len(page.LeadHandling) != 0 || len(waitingTurns(page)) != 2 {
		t.Fatalf("with its lead itself waiting on the person, the worker's wait is not the person's: %+v", page)
	}

	if err := f.r.ArchiveSession("lead", time.Now()); err != nil {
		t.Fatal(err)
	}
	page = freshDecisions(t, f.d)
	turns := waitingTurns(page)
	if len(turns) != 1 || len(page.LeadHandling) != 0 {
		t.Fatalf("with its lead archived, the worker's wait is not the person's: %+v", page)
	}
	if turn := turns[0]; turn.WorkItem != added.Item.ID || turn.WorkTitle != "import the March export" || turn.LedgerKey != key {
		t.Fatalf("the wait does not name the work it holds up: %+v", turn)
	}
}

func workerTurn(t *testing.T, f *mcpAnswerFixture, session *mcp.ClientSession, id string, end map[string]any) {
	t.Helper()
	worker := f.surface.sessions["worker"]
	for _, observation := range []*surface.TurnObservation{{Status: surface.StatusIdle}, {Status: surface.StatusBusy, ActiveTurnID: id}} {
		f.surface.observations["worker"] = observation
		f.d.observeSession(t.Context(), f.surface, &worker)
	}
	if out, refused := callMCP(t, session, "agenthail_end_turn", end); refused {
		t.Fatalf("end turn refused: %v", out)
	}
	f.surface.observations["worker"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: id, Reply: &surface.ReplyResult{Text: "reply", Done: true}}
	f.d.observeSession(t.Context(), f.surface, &worker)
	f.d.background.wait()
}
