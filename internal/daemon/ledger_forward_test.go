package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/buildinfo"
	"github.com/zm2231/agenthail/internal/ledger"
)

func release(version string) buildinfo.Identity {
	return buildinfo.Identity{Version: version, Revision: strings.Repeat("ab", 20)}
}

func ownedLedgerOnTwoHosts(t *testing.T, macBuild, ownerBuild buildinfo.Identity) (mac, owner *federationNode) {
	t.Helper()
	mac, owner, _ = ledgerOnTwoHostsBuilt(t, macBuild, ownerBuild)
	if code, result, body := assignOwner(t, mac); code != http.StatusOK || !result.Confirmed {
		t.Fatalf("assignment: status=%d body=%s", code, body)
	}
	return mac, owner
}

func ownersItem(t *testing.T, owner *federationNode, id string) ledger.WorkItem {
	t.Helper()
	board, err := owner.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	item, found := board.WorkItem(id)
	if !found {
		t.Fatalf("the owner has no %s", id)
	}
	return item
}

func TestEveryWorkActionFromAnotherHostIsRecordedByTheLedgersOwner(t *testing.T) {
	mac, owner := ownedLedgerOnTwoHosts(t, release("v0.2.170"), release("v0.2.170"))
	work := func(session string, req ledger.WorkRequest) ledger.WorkItem {
		t.Helper()
		req.ProjectKey, req.SessionID = widgetKey, session
		var result ledger.WorkResult
		mustLedger(t, mac, "/api/v1/ledger/work", req, &result)
		return result.Item
	}
	said := capturePrompt(t, mac, "local-a", "p-1", "fix the flag")
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Flags"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: widgetKey, SessionID: "local-a", UtteranceID: said, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"fix the flag"}}, nil)
	board, err := owner.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := board.Clause("widget-C1"); !found {
		t.Fatal("the owner has no clause for the person's words")
	}
	item := work("local-a", ledger.WorkRequest{Action: ledger.WorkAdd, Text: "fix the flag", Fields: ledger.WorkFields{Type: ledger.WorkBug, Priority: "P1"}}).ID
	other := work("local-a", ledger.WorkRequest{Action: ledger.WorkAdd, Text: "write the docs"}).ID
	steps := []struct {
		session string
		req     ledger.WorkRequest
		check   func(ledger.WorkItem) bool
	}{
		{"local-a", ledger.WorkRequest{Action: ledger.WorkLink, ClauseID: "widget-C1"}, func(i ledger.WorkItem) bool { return i.ClauseID == "widget-C1" }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkEdit, Change: ledger.WorkChange{Description: ptr("the flag is read twice")}}, func(i ledger.WorkItem) bool { return i.Description == "the flag is read twice" }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkDep, DependsOn: other}, func(i ledger.WorkItem) bool { return len(i.DependsOn) == 1 }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkUndep, DependsOn: other}, func(i ledger.WorkItem) bool { return len(i.DependsOn) == 0 }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkComment, Text: "seen on the laptop too"}, func(i ledger.WorkItem) bool { return lastKind(i) == "comment" }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkClaim}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInProgress && i.Assignee == "local-a" }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkLog, Text: "read it once", LogType: "decision"}, func(i ledger.WorkItem) bool { return lastKind(i) == "log" }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkBlock, Reason: "waits on the review of the parser"}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkBlocked }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkHandoff, Handoff: ledger.Handoff{Done: []string{"found the double read"}, Decisions: []string{"read it once at start"}}}, func(i ledger.WorkItem) bool {
			return i.State == ledger.WorkOpen && i.Assignee == "" && i.Handoff != nil && i.Handoff.Done[0] == "found the double read" && i.Handoff.Decisions[0] == "read it once at start"
		}},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkClaim}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInProgress }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkRelease, Reason: "moving to the parser first"}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkOpen && i.Assignee == "" }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkClaim}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInProgress }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkPark, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "after the release"}, Handoff: ledger.Handoff{Remaining: []string{"the test"}}}, func(i ledger.WorkItem) bool {
			return i.State == ledger.WorkParked && i.Handoff != nil && i.Handoff.Remaining[0] == "the test"
		}},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkUnpark}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkOpen }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkClaim}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInProgress }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkProof, Proof: &ledger.ProofWrite{Op: ledger.ProofAdd, OperationID: "proof-1", Step: ledger.ProofStep{Title: "Read the flag", Expected: "one read", Observed: "one read", Outcome: ledger.ProofPass}}}, func(i ledger.WorkItem) bool { return len(i.Proof) == 1 }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkSubmit}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInReview }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkReject, Reason: "needs the test"}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInProgress }},
		{"local-a", ledger.WorkRequest{Action: ledger.WorkSubmit}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkInReview }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkApprove}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkDone && i.Reviewer == "lead" }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkReopen}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkOpen }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkClose, Reason: "obsolete: the flag is gone"}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkClosed }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkDelete}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkDeleted }},
		{"lead", ledger.WorkRequest{Action: ledger.WorkRestore}, func(i ledger.WorkItem) bool { return i.State == ledger.WorkOpen }},
	}
	for _, step := range steps {
		step.req.ItemID = item
		work(step.session, step.req)
		if got := ownersItem(t, owner, item); !step.check(got) {
			t.Fatalf("after %s from the other host, the owner has %+v", step.req.Action, got)
		}
	}

	work("local-a", ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: item})
	if err := mac.reg.ArchiveSession("local-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	work("lead", ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: item, Reason: "its session was archived", Takeover: &ledger.Takeover{Then: ledger.TakeoverRelease}})
	got := ownersItem(t, owner, item)
	if last := got.Timeline[len(got.Timeline)-1]; got.State != ledger.WorkOpen || got.Assignee != "" || last.Actor != "lead" || !strings.Contains(last.Text, "taken over from local-a (archived)") {
		t.Fatalf("after a takeover from the other host, the owner has %+v", got)
	}
}

func TestAHolderOnAnotherHostHandsOffAndReleasesOverMCP(t *testing.T) {
	mac, owner := ownedLedgerOnTwoHosts(t, release("v0.2.170"), release("v0.2.170"))
	caller, err := mac.reg.Session("local-a")
	if err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, mac.daemon), issueMCPToken(t, mac.daemon, *caller))
	calls := 0
	write := func(request map[string]any) (map[string]any, bool) {
		t.Helper()
		calls++
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": fmt.Sprintf("w%d", calls), "request": request})
	}
	out, refused := write(map[string]any{"action": "add", "text": "fix the flag"})
	if refused {
		t.Fatalf("add: %v", out)
	}
	item := out["item"].(map[string]any)["id"].(string)
	if out, refused := write(map[string]any{"action": "claim", "itemId": item}); refused {
		t.Fatalf("claim: %v", out)
	}

	out, refused = write(map[string]any{"action": "handoff", "itemId": item, "done": []string{"found it"}, "decisions": []string{"read once"}})
	message := fmt.Sprint(out["message"])
	if !refused || out["code"] != "invalid" || !strings.Contains(message, `"decisions" as handoff.decisions`) || !strings.Contains(message, `"done" as handoff.done`) {
		t.Fatalf("a handoff with its fields at the top of the request = %v", out)
	}
	if got := ownersItem(t, owner, item); got.State != ledger.WorkInProgress || got.Handoff != nil {
		t.Fatalf("the owner recorded a refused handoff: %+v", got)
	}

	if out, refused := write(map[string]any{"action": "handoff", "itemId": item, "handoff": map[string]any{"done": []string{"found it"}, "decisions": []string{"read once"}}}); refused {
		t.Fatalf("handoff: %v", out)
	}
	if got := ownersItem(t, owner, item); got.State != ledger.WorkOpen || got.Handoff == nil || got.Handoff.Decisions[0] != "read once" || got.Handoff.By != "local-a" {
		t.Fatalf("the owner's item after the handoff: %+v", got)
	}
	if out, refused := write(map[string]any{"action": "claim", "itemId": item}); refused {
		t.Fatalf("claim again: %v", out)
	}
	if out, refused := write(map[string]any{"action": "release", "itemId": item}); refused {
		t.Fatalf("release: %v", out)
	}
	if got := ownersItem(t, owner, item); got.State != ledger.WorkOpen || got.Assignee != "" {
		t.Fatalf("the owner's item after the release: %+v", got)
	}
}

func TestOnlyTheLeadOnAnotherHostPassesAReviewOnToTheLedgersOwner(t *testing.T) {
	mac, owner := ownedLedgerOnTwoHosts(t, release("v0.2.170"), release("v0.2.170"))
	writer := func(sessionID string) func(map[string]any) (map[string]any, bool) {
		caller, err := mac.reg.Session(sessionID)
		if err != nil {
			t.Fatal(err)
		}
		session := mcpSession(t, mcpServer(t, mac.daemon), issueMCPToken(t, mac.daemon, *caller))
		calls := 0
		return func(request map[string]any) (map[string]any, bool) {
			t.Helper()
			calls++
			return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": fmt.Sprintf("%s-%d", sessionID, calls), "request": request})
		}
	}
	worker, lead := writer("local-a"), writer("lead")
	out, refused := worker(map[string]any{"action": "add", "text": "pick the import window"})
	if refused {
		t.Fatalf("add: %v", out)
	}
	item := out["item"].(map[string]any)["id"].(string)
	for _, action := range []string{"claim", "submit"} {
		if out, refused := worker(map[string]any{"action": action, "itemId": item}); refused {
			t.Fatalf("%s: %v", action, out)
		}
	}

	out, refused = worker(map[string]any{"action": "review-pass", "itemId": item, "reason": "Pick the import window"})
	if !refused || !strings.Contains(fmt.Sprint(out["message"]), "only the lead") {
		t.Fatalf("a review-pass from a session that does not lead the project = %v", out)
	}
	if got := ownersItem(t, owner, item); got.ReviewPass != nil {
		t.Fatalf("the owner recorded a review-pass the lead did not make: %+v", got.ReviewPass)
	}

	if out, refused := lead(map[string]any{"action": "review-pass", "itemId": item, "reason": "Pick the import window"}); refused {
		t.Fatalf("the lead's review-pass: %v", out)
	}
	got := ownersItem(t, owner, item)
	if got.State != ledger.WorkInReview || got.ReviewPass == nil || got.ReviewPass.By != "lead" || got.ReviewPass.Reason != "Pick the import window" || lastKind(got) != "review-pass" {
		t.Fatalf("the owner's item after the lead's review-pass: %+v", got)
	}
}

func lastKind(item ledger.WorkItem) string {
	return item.Timeline[len(item.Timeline)-1].Kind
}

func ptr[T any](value T) *T { return &value }
