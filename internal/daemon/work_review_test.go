package daemon

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func (f *mcpAnswerFixture) submitWork(t *testing.T, text string) (string, string) {
	t.Helper()
	store, key := f.d.ledgerStore(), ledgerKeyAt(t, f.r, f.surface.sessions["worker"].Cwd)
	added, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkAdd, Text: text}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id := added.Item.ID
	steps := []ledger.WorkRequest{
		{Action: ledger.WorkClaim},
		{Action: ledger.WorkProof, Proof: &ledger.ProofWrite{Op: ledger.ProofAdd, OperationID: "step-1", Step: ledger.ProofStep{Title: "totals match", Expected: "the March totals", Observed: "they match", Outcome: ledger.ProofPass}}},
		{Action: ledger.WorkSubmit},
	}
	for _, step := range steps {
		step.ProjectKey, step.SessionID, step.ItemID = key, "worker", id
		if _, err := store.Work(step, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return key, id
}

func reviewsOn(page decisionsPage) []decisionItem {
	var reviews []decisionItem
	for _, item := range page.Decisions {
		if item.Kind == decisionReview {
			reviews = append(reviews, item)
		}
	}
	return reviews
}

func (f *mcpAnswerFixture) sweepReviews(t *testing.T) decisionsPage {
	t.Helper()
	f.d.discoverCatalog(t.Context())
	f.d.sweepReviewPushes(t.Context())
	f.d.background.wait()
	return freshDecisions(t, f.d)
}

func reviewPass(t *testing.T, session *mcp.ClientSession, itemID, reason string) (map[string]any, bool) {
	t.Helper()
	return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "pass-" + itemID + "-" + reason, "request": map[string]any{"action": "review-pass", "itemId": itemID, "reason": reason}})
}

func (f *mcpAnswerFixture) review(t *testing.T, p principal, key, itemID, verdict, note string) actionReply {
	t.Helper()
	return f.d.doAction(t.Context(), p, dashboardActionRequest{Action: workReviewAction, LedgerKey: key, WorkItem: itemID, Verdict: verdict, Note: note})
}

func TestAReviewInAProjectWithoutALeadIsThePersonsAndPushesOnce(t *testing.T) {
	f := newMCPAnswerFixture(t)
	pushes := newPushRecorder(t, f.r)
	key, id := f.submitWork(t, "Import the March export")

	page := f.sweepReviews(t)
	reviews := reviewsOn(page)
	if len(reviews) != 1 || len(page.LeadHandling) != 0 {
		t.Fatalf("the submitted item is not one review for the person: %+v", page)
	}
	review := reviews[0]
	if review.Key != "review:"+key+":"+id || review.Group != decisionReview || review.SessionID != "worker" || review.WorkTitle != "Import the March export" ||
		review.Phase != ledger.PhaseCheck || review.Proof == nil || *review.Proof != (ledger.ProofCounts{Pass: 1}) || review.Answerable {
		t.Fatalf("review = %+v", review)
	}
	if push := pushes.all(workReviewEvent); len(push) != 1 || push[0]["message"] != "Import the March export is ready for your review" {
		t.Fatalf("the review pushed %+v", push)
	}
	f.sweepReviews(t)
	if count := pushes.count(workReviewEvent); count != 1 {
		t.Fatalf("a review pushed again on a later sweep: %d", count)
	}
}

func TestAReviewStaysWithTheLeadUntilTheLeadPassesItOn(t *testing.T) {
	f := newMCPAnswerFixture(t)
	pushes := newPushRecorder(t, f.r)
	lead := f.asProjectLead(t, "lead")
	_, id := f.submitWork(t, "Import the March export")

	page := f.sweepReviews(t)
	if len(reviewsOn(page)) != 0 || len(page.LeadHandling) != 1 || page.LeadHandling[0].WorkItem != id || page.LeadHandling[0].HeldBy != "lead" || pushes.count(workReviewEvent) != 0 {
		t.Fatalf("a review in a project with a lead left the lead: %+v, %d pushes", page, pushes.count(workReviewEvent))
	}
	if out, refused := reviewPass(t, f.worker, id, "I want the person"); !refused {
		t.Fatalf("a worker passed its own review on: %v", out)
	}
	if out, refused := reviewPass(t, lead, id, "Pick the import window"); refused {
		t.Fatalf("the lead could not pass the review on: %v", out)
	}
	page = f.sweepReviews(t)
	if reviews := reviewsOn(page); len(reviews) != 1 || reviews[0].Reason != "Pick the import window" || len(page.LeadHandling) != 0 {
		t.Fatalf("a passed review is not the person's: %+v", page)
	}
	if count := pushes.count(workReviewEvent); count != 1 {
		t.Fatalf("a passed review pushed %d times", count)
	}
}

func TestALedgerChangeThatMovesAReviewPublishesANewDecisionsRevision(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.asProjectLead(t, "lead")
	key, id := f.submitWork(t, "Import the March export")
	f.d.discoverCatalog(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go f.d.runDecisionsRevision(ctx)
	testkit.Eventually(t, "the first revision published", 10*time.Second, func() bool { return len(decisionsEvents(t, f.d)) > 0 })
	before, _ := f.d.decisions.current()

	if _, err := f.d.ledgerStore().Work(ledger.WorkRequest{ProjectKey: key, SessionID: "lead", Action: ledger.WorkReviewPass, ItemID: id, Reason: "Pick the import window"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	moved := ownRevision(t, f.d)
	if moved == before {
		t.Fatal("a passed review left the decisions page as it was")
	}
	testkit.Eventually(t, "the passed review's revision published", 10*time.Second, func() bool {
		events := decisionsEvents(t, f.d)
		return events[len(events)-1]["own"] == moved
	})
}

func TestAReviewMovesToThePersonWhenTheLeadIsGone(t *testing.T) {
	f := newMCPAnswerFixture(t)
	pushes := newPushRecorder(t, f.r)
	f.asProjectLead(t, "lead")
	f.submitWork(t, "Import the March export")
	if err := f.r.ArchiveSession("lead", time.Now()); err != nil {
		t.Fatal(err)
	}
	if page := f.sweepReviews(t); len(reviewsOn(page)) != 1 || len(page.LeadHandling) != 0 || pushes.count(workReviewEvent) != 1 {
		t.Fatalf("with its lead archived the review did not reach the person: %+v, %d pushes", page, pushes.count(workReviewEvent))
	}
}

func TestALeadWhoseTurnEndedWaitingOnThePersonStillHoldsAReview(t *testing.T) {
	f := newMCPAnswerFixture(t)
	pushes := newPushRecorder(t, f.r)
	leadSession := f.asProjectLead(t, "lead")
	_, id := f.submitWork(t, "Import the March export")
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

	page := f.sweepReviews(t)
	if len(reviewsOn(page)) != 0 || !slices.ContainsFunc(page.LeadHandling, func(held leadHandledItem) bool { return held.WorkItem == id && held.HeldBy == "lead" }) || pushes.count(workReviewEvent) != 0 {
		t.Fatalf("a lead waiting on the person for something else lost the review: %+v, %d pushes", page, pushes.count(workReviewEvent))
	}
}

func TestThePersonSendsBackAndApprovesAReviewInTheirOwnWords(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, id := f.submitWork(t, "Import the March export")
	person := principal{ID: localDashboardPrincipal, Kind: principalPerson}

	if reply := f.review(t, sessionPrincipal("lead"), key, id, reviewApprove, ""); reply.Status != http.StatusForbidden {
		t.Fatalf("a session's work-review answered %d %s", reply.Status, reply.Body)
	}
	if reply := f.review(t, person, key, id, reviewReject, ""); reply.Status != http.StatusBadRequest {
		t.Fatalf("a send back without a note answered %d %s", reply.Status, reply.Body)
	}
	if reply := f.review(t, person, key, id, reviewReject, "The totals are off"); reply.Status != http.StatusOK {
		t.Fatalf("send back answered %d %s", reply.Status, reply.Body)
	}
	item := f.workItem(t, key, id)
	if item.State != ledger.WorkInProgress || item.Reviewer != surface.PersonSessionID || item.Reason != "The totals are off" {
		t.Fatalf("after the send back the item reads %+v", item)
	}
	if page := freshDecisions(t, f.d); len(reviewsOn(page)) != 0 {
		t.Fatalf("an item sent back is still a review: %+v", page)
	}
	if reply := f.review(t, person, key, id, reviewReject, "The totals are off"); reply.Status != http.StatusConflict {
		t.Fatalf("a review of an item out of review answered %d %s", reply.Status, reply.Body)
	}

	if _, err := f.d.ledgerStore().Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkSubmit, ItemID: id}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if reply := f.review(t, person, key, id, reviewApprove, ""); reply.Status != http.StatusOK {
		t.Fatalf("approve answered %d %s", reply.Status, reply.Body)
	}
	if item := f.workItem(t, key, id); item.State != ledger.WorkDone || item.Reviewer != surface.PersonSessionID {
		t.Fatalf("after the approval the item reads %+v", item)
	}
	if texts := utteranceTexts(t, f.d.ledgerStore(), key); !slices.Contains(texts, "Sent back. The totals are off") || !slices.Contains(texts, "Approved.") {
		t.Fatalf("the person's words are not recorded: %q", texts)
	}
}

func (f *mcpAnswerFixture) workItem(t *testing.T, key, id string) ledger.WorkItem {
	t.Helper()
	board, err := f.d.ledgerStore().Board(key)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := board.WorkItem(id)
	return item
}
