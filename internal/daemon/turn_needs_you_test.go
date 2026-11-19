package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func judgedTurn(t *testing.T, d *Daemon, fake *daemonSurface, session *surface.Session, turnID, reply string) registry.TurnJudgment {
	t.Helper()
	finishJudgedTurn(d, fake, session, turnID, reply, nil)
	var judged registry.TurnJudgment
	eventually(t, "the judgment of "+turnID, func() bool {
		for _, j := range readJudgments(t, d, session.ID).Judgments {
			if j.TurnID == turnID && !j.JudgedAt.IsZero() {
				judged = j
				return true
			}
		}
		return false
	})
	d.background.wait()
	return judged
}

func submitPrompt(t *testing.T, d *Daemon, sessionID, promptID, prompt string) {
	t.Helper()
	if err := d.Registry.OpenTurn(sessionID, time.Now()); err != nil {
		t.Fatal(err)
	}
	api := signinAPIClient{t: t, handler: dashboardRouter(d), token: dashboardTestToken}
	body, _ := json.Marshal(ledgerCaptureRequest{SessionID: sessionID, PromptID: promptID, Prompt: prompt})
	var answer ledgerCaptureResponse
	if code := api.do(http.MethodPost, "/api/v1/ledger/capture", string(body), &answer); code != http.StatusOK {
		t.Fatalf("the prompt hook's capture answered %d", code)
	}
}

func TestATurnTheJudgeSaysNeedsThePersonPushesNeedsYouInPlaceOfItsFinishedPush(t *testing.T) {
	stub := &judgeStub{state: turnWaitingPerson}
	d, r, fake, from := judgeFixture(t, stub, "")
	pushes := newPushRecorder(t, r)

	judged := judgedTurn(t, d, fake, &from, "turn-1", "The fix is ready. Should I deploy it now?")
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("a self-started turn waiting on the person pushed %d times", count)
	}
	push := pushes.first(turnNeedsYouEvent)
	if push["message"] != "Needs you" || push["title"] != "Codex agent" || push["sessionId"] != from.ID || push["turnId"] != "turn-1" || push["dedupeKey"] == "" || push["collapseId"] == "" {
		t.Fatalf("needs-you push %+v", push)
	}
	if finished := pushes.all("turn.completed"); len(finished) != 0 {
		t.Fatalf("the turn pushed finished %+v as well as needs-you %+v", finished, push)
	}
	if row := viewRow(t, d, from.ID); row.TurnState == nil || row.TurnState.TurnID != "turn-1" || !row.TurnState.Pushed {
		t.Fatalf("the row does not say its turn was pushed: %+v", row.TurnState)
	}

	d.recordTurnJudgment(judged)
	d.background.wait()
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("judging the same turn again pushed it %d times", count)
	}
}

func TestOnlyATurnThePersonIsAwayFromPushesNeedsYou(t *testing.T) {
	stub := &judgeStub{state: turnWaitingPerson}
	d, r, fake, from := judgeFixture(t, stub, "")
	pushes := newPushRecorder(t, r)

	submitPrompt(t, d, from.ID, "typed-1", "fix the parser")
	if judged := judgedTurn(t, d, fake, &from, "turn-1", "Fixed. Should I deploy it?"); !judged.Facts.Typed {
		t.Fatalf("a prompt typed into the session is not recorded as typed: %+v", judged.Facts)
	}
	if count := pushes.count(turnNeedsYouEvent); count != 0 {
		t.Fatalf("a turn the person typed at the terminal pushed %d times", count)
	}

	d.personCapture(delivery.PersonCaptureContext{Target: from, IntentUUID: "intent-2", Message: "deploy it"})
	submitPrompt(t, d, from.ID, "delivered-2", "deploy it")
	if judged := judgedTurn(t, d, fake, &from, "turn-2", "Deployed to staging. Promote it to production?"); judged.Facts.Typed {
		t.Fatalf("a prompt the person sent through Agenthail is recorded as typed: %+v", judged.Facts)
	}
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("a turn the person started from their phone pushed %d times", count)
	}

	for _, state := range []string{turnDone, turnWaitingWork, turnContinuing} {
		stub.mu.Lock()
		stub.state = state
		stub.mu.Unlock()
		judgedTurn(t, d, fake, &from, "turn-"+state, "Done.")
	}
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("turns judged done, waiting on work or continuing pushed: %d needs-you pushes", count)
	}
}

func TestAJudgedNeedsYouTurnOnAPairedHostReachesThePhoneOnce(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, host.reg, "phone", "via-host")
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")

	hostTurn(t, host, "remote-a", "turn-1", "", nil)
	host.daemon.recordTurnJudgment(registry.TurnJudgment{SessionID: "remote-a", TurnID: "turn-1", EndedAt: time.Now(), JudgedAt: time.Now(), State: turnWaitingPerson})
	host.daemon.discoverCatalog(t.Context())
	needsYou := func() []map[string]string {
		var sends []map[string]string
		for _, send := range relay.sent() {
			if send["eventType"] == turnNeedsYouEvent {
				sends = append(sends, send)
			}
		}
		return sends
	}
	testkit.Eventually(t, "both computers to push the needs-you turn", 10*time.Second, func() bool { return len(needsYou()) == 2 })
	settleNotices(t, host, record.ID, viewer)
	sends := needsYou()
	if sends[0]["dedupeKey"] == "" || sends[0]["dedupeKey"] != sends[1]["dedupeKey"] || sends[0]["collapseId"] != sends[1]["collapseId"] {
		t.Fatalf("the host and the viewer named the needs-you push differently: %+v", sends)
	}
	var shown []map[string]string
	for _, push := range relay.shown("phone") {
		if push["eventType"] == turnNeedsYouEvent {
			shown = append(shown, push)
		}
	}
	if len(shown) != 1 {
		t.Fatalf("the phone showed the needs-you turn %d times: %+v", len(shown), shown)
	}
	var said []string
	for _, notice := range mac.list() {
		if strings.Contains(notice.message, "Needs you") {
			said = append(said, notice.message)
		}
	}
	if len(said) != 1 || said[0] != "Needs you" {
		t.Fatalf("the viewer's desktop said %q", said)
	}

	settleNotices(t, host, record.ID, viewer)
	if sends := needsYou(); len(sends) != 2 {
		t.Fatalf("a later change to the row pushed the turn again: %+v", sends)
	}

	hostTurn(t, host, "remote-a", "turn-2", "", nil)
	host.daemon.recordTurnJudgment(registry.TurnJudgment{SessionID: "remote-a", TurnID: "turn-2", EndedAt: time.Now(), JudgedAt: time.Now(), State: turnWaitingPerson, Facts: registry.TurnFacts{Typed: true}})
	host.daemon.discoverCatalog(t.Context())
	settleNotices(t, host, record.ID, viewer)
	if sends := needsYou(); len(sends) != 2 {
		t.Fatalf("a turn its host did not push, since the person typed it there, was pushed by the viewer: %+v", sends)
	}
}

func TestALeadNamedWhileItsTurnIsBeingPushedStopsThePushAndNoRowShowsItPushed(t *testing.T) {
	mac, _, _ := widgetOnTwoHosts(t)
	pushes := newPushRecorder(t, mac.reg)
	spec, err := mac.reg.Coordinator("repo:github.com/example/widget")
	if err != nil || spec.SessionID != "lead" {
		t.Fatalf("coordinator %+v err=%v", spec, err)
	}
	var claimed *dashboardTurnState
	mac.daemon.turnPushRead = func() {
		mac.daemon.turnPushRead = nil
		claimed = viewRow(t, mac.daemon, "lead").TurnState
		if _, _, err := mac.reg.ReplaceCoordinator(spec, registry.CoordinatorChange{SessionID: "local-a", Replace: true, OverPeers: true, ChangedBy: "fixture"}); err != nil {
			t.Error(err)
		}
	}
	judgeTurnOf(t, mac, "lead", "lead-1", registry.TurnJudgment{State: turnWaitingPerson})
	mac.daemon.background.wait()
	if claimed == nil || claimed.State != turnWaitingPerson || claimed.Pushed {
		t.Fatalf("while its push was claimed but not yet sent the row read %+v", claimed)
	}
	if count := pushes.count(turnNeedsYouEvent); count != 0 {
		t.Fatalf("a session that became a worker while its turn was pushed pushed %d times", count)
	}
	if state := viewRow(t, mac.daemon, "lead").TurnState; state == nil || state.State != turnWaitingWork || state.Pushed {
		t.Fatalf("the new worker's row reads %+v", state)
	}

	judgeTurnOf(t, mac, "local-a", "a-1", registry.TurnJudgment{State: turnWaitingPerson})
	mac.daemon.background.wait()
	if count := pushes.count(turnNeedsYouEvent); count != 1 {
		t.Fatalf("the new lead waiting on the person pushed %d times", count)
	}
}
