package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func leadWaitFixture(t *testing.T) (*mcpAnswerFixture, *pushRecorder) {
	t.Helper()
	f := newMCPAnswerFixture(t)
	pushes := newPushRecorder(t, f.r)
	f.asProjectLead(t, "lead")
	return f, pushes
}

func (f *mcpAnswerFixture) holdWork(t *testing.T, text string) {
	t.Helper()
	store, key := f.d.ledgerStore(), ledgerKeyAt(t, f.r, f.surface.sessions["worker"].Cwd)
	added, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkAdd, Text: text}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkClaim, ItemID: added.Item.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func (f *mcpAnswerFixture) sweepLeadWaits(t *testing.T, after time.Duration) decisionsPage {
	t.Helper()
	f.d.discoverCatalog(t.Context())
	f.d.sweepLeadWaits(t.Context(), time.Now().Add(after))
	f.d.background.wait()
	return freshDecisions(t, f.d)
}

func (f *mcpAnswerFixture) row(t *testing.T, id string) dashboardSession {
	t.Helper()
	f.d.discoverCatalog(t.Context())
	rows, _, err := f.d.decisionRows(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("%s is not among the sessions", id)
	return dashboardSession{}
}

func TestAWorkersWaitOnItsLeadMovesToThePersonAfterTheAskWindow(t *testing.T) {
	f, pushes := leadWaitFixture(t)
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Ready", "waitingOn": "Approve the schema change"})

	page := f.sweepLeadWaits(t, 9*time.Minute)
	if len(waitingTurns(page)) != 0 || len(page.LeadHandling) != 1 || pushes.count(turnNeedsYouEvent) != 0 {
		t.Fatalf("inside the ask window the wait left the lead: %+v, %d pushes", page, pushes.count(turnNeedsYouEvent))
	}

	page = f.sweepLeadWaits(t, 11*time.Minute)
	turns := waitingTurns(page)
	if len(turns) != 1 || len(page.LeadHandling) != 0 || turns[0].Reason != "Approve the schema change" || turns[0].Key != "turn:worker:turn-1" {
		t.Fatalf("after the ask window the wait is not the person's: %+v", page)
	}
	if push := pushes.all(turnNeedsYouEvent); len(push) != 1 || push[0]["message"] != "Needs you: Approve the schema change" {
		t.Fatalf("the moved wait pushed %+v", push)
	}
	if row := f.row(t, "worker"); row.TurnState == nil || row.TurnState.State != turnWaitingPerson || !row.TurnState.Escalated || !row.TurnState.Pushed {
		t.Fatalf("the worker's row = %+v", row.TurnState)
	}

	f.sweepLeadWaits(t, time.Hour)
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("a moved wait pushed again on a later sweep: %d", count)
	}
}

func TestAWaitReadFromTheTurnMovesAfterItsOwnLongerWindowOnlyWhileTheWorkerHoldsWork(t *testing.T) {
	f, pushes := leadWaitFixture(t)
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Sent the plan for an ack"})
	if err := f.r.RecordTurnJudgment(registry.TurnJudgment{SessionID: "worker", TurnID: "turn-1", EndedAt: time.Now(), State: turnWaitingPerson, Headline: "Sent the plan for an ack"}); err != nil {
		t.Fatal(err)
	}
	f.d.background.wait()
	if row := f.row(t, "worker"); row.TurnState == nil || row.TurnState.State != turnWaitingWork || !row.TurnState.OnLead {
		t.Fatalf("the judged wait of a worker is not on its lead: %+v", row.TurnState)
	}

	if page := f.sweepLeadWaits(t, 3*time.Hour); len(waitingTurns(page)) != 0 || pushes.count(turnNeedsYouEvent) != 0 {
		t.Fatalf("a worker holding no work had its judged wait moved: %+v", page)
	}

	f.holdWork(t, "write the plan")
	if page := f.sweepLeadWaits(t, 11*time.Minute); len(waitingTurns(page)) != 0 {
		t.Fatalf("a judged wait moved inside its window: %+v", page)
	}
	if page := f.sweepLeadWaits(t, 119*time.Minute); len(waitingTurns(page)) != 0 {
		t.Fatalf("a judged wait moved before the default two hours: %+v", page)
	}
	page := f.sweepLeadWaits(t, 121*time.Minute)
	turns := waitingTurns(page)
	if len(turns) != 1 || turns[0].Reason != "@captain has not answered: Sent the plan for an ack" || turns[0].Key != "turn:worker:turn-1" {
		t.Fatalf("after two hours the judged wait is not the person's: %+v", page)
	}
	if push := pushes.all(turnNeedsYouEvent); len(push) != 1 || push[0]["message"] != "Needs you: @captain has not answered: Sent the plan for an ack" {
		t.Fatalf("the moved judged wait pushed %+v", push)
	}
}

func TestAWaitDoesNotMoveWhileTheWorkersPullRequestRunsItsChecks(t *testing.T) {
	f, pushes := leadWaitFixture(t)
	pr := registry.SessionPR{SessionID: "worker", Repo: "github.com/acme/app", Number: 7, URL: "https://github.com/acme/app/pull/7", Source: registry.PRSourceAgent}
	if _, err := f.r.LinkPR(pr, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	pr.State, pr.Checks, pr.Watch = "open", "pending", true
	if recorded, err := f.r.RecordPRRead(pr, 0, time.Now()); err != nil || !recorded {
		t.Fatalf("record the read: %v %v", recorded, err)
	}
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Ready", "waitingOn": "Merge once CI passes"})

	if page := f.sweepLeadWaits(t, 3*time.Hour); len(waitingTurns(page)) != 0 || pushes.count(turnNeedsYouEvent) != 0 {
		t.Fatalf("a wait moved while its pull request ran its checks: %+v", page)
	}

	pr.Checks = "passing"
	if recorded, err := f.r.RecordPRRead(pr, 0, time.Now()); err != nil || !recorded {
		t.Fatalf("record the read: %v %v", recorded, err)
	}
	if page := f.sweepLeadWaits(t, 3*time.Hour); len(waitingTurns(page)) != 1 || pushes.count(turnNeedsYouEvent) != 1 {
		t.Fatalf("once the checks finished the overdue wait did not move: %+v", page)
	}
}

func TestAWaitMovesAtOnceWhenTheLeadIsGoneAndPushesOnce(t *testing.T) {
	f, pushes := leadWaitFixture(t)
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Ready", "waitingOn": "Approve the schema change"})
	if err := f.r.ArchiveSession("lead", time.Now()); err != nil {
		t.Fatal(err)
	}
	page := f.sweepLeadWaits(t, 0)
	if len(waitingTurns(page)) != 1 || pushes.count(turnNeedsYouEvent) != 1 {
		t.Fatalf("with its lead archived the wait did not move and push at once: %+v, %d pushes", page, pushes.count(turnNeedsYouEvent))
	}
}

func TestTheLedgerGateTellsAWorkerToAskItsLead(t *testing.T) {
	blocked := []ledger.GateResult{{ProjectKey: "app", Blocked: []string{"c1"}}}
	if notice := gateNotice(blocked, "@captain"); !strings.Contains(notice, "agenthail_ask") || !strings.Contains(notice, "@captain") {
		t.Fatalf("a worker's gate notice does not point it to an ask: %s", notice)
	}
	if notice := gateNotice(blocked, ""); strings.Contains(notice, "agenthail_ask") {
		t.Fatalf("a session without a lead is told to ask one: %s", notice)
	}
}

func TestAMovedWaitDoesNotPushWhenTheWorkerBecomesTheLeadWhileItIsClaimed(t *testing.T) {
	f, pushes := leadWaitFixture(t)
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Ready", "waitingOn": "Approve the schema change"})
	f.d.turnPushRead = func() {
		f.d.turnPushRead = nil
		person := principal{ID: localDashboardPrincipal, Kind: principalPerson}
		if reply := f.d.doAction(t.Context(), person, dashboardActionRequest{Action: "coordinator-set", SessionID: "worker", Name: "mate", Replace: true}); reply.Status != 200 {
			t.Fatalf("coordinator-set status=%d %s", reply.Status, reply.Body)
		}
	}
	f.sweepLeadWaits(t, 11*time.Minute)
	if count := pushes.count(turnNeedsYouEvent); count != 0 {
		t.Fatalf("a wait that stopped being a moved one while its push was claimed pushed %d times", count)
	}
	if end, found, err := f.r.CompletedTurnEnd("worker", "turn-1"); err != nil || !found || !end.WaitPushed.IsZero() {
		t.Fatalf("the claim was not released: %+v %v %v", end, found, err)
	}
}

func TestAWorkerAwaitingItsLeadMovesAfterTheLongerWindow(t *testing.T) {
	f, pushes := leadWaitFixture(t)
	f.holdWork(t, "write the plan")
	workerTurn(t, f, f.worker, "turn-1", map[string]any{"headline": "Asked the lead for an ack"})
	if err := f.r.RecordTurnJudgment(registry.TurnJudgment{SessionID: "worker", TurnID: "turn-1", EndedAt: time.Now(), State: turnDone, Headline: "Asked the lead for an ack", Facts: registry.TurnFacts{Awaiting: []string{"lead"}, AwaitedRelays: 1}}); err != nil {
		t.Fatal(err)
	}
	if row := f.row(t, "worker"); row.TurnState == nil || row.TurnState.Source != turnSourceFacts || !row.TurnState.OnLead {
		t.Fatalf("a worker awaiting its lead is not waiting on it: %+v", row.TurnState)
	}
	if page := f.sweepLeadWaits(t, 119*time.Minute); len(waitingTurns(page)) != 0 {
		t.Fatalf("a wait on the lead from the facts moved inside two hours: %+v", page)
	}
	page := f.sweepLeadWaits(t, 121*time.Minute)
	if turns := waitingTurns(page); len(turns) != 1 || turns[0].Reason != "@captain has not answered: Asked the lead for an ack" || pushes.count(turnNeedsYouEvent) != 1 {
		t.Fatalf("after two hours the wait from the facts is not the person's: %+v", page)
	}
}
