package daemon

import (
	"log"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func settledLines(l *lockedLog) []string {
	var found []string
	for _, line := range strings.Split(l.String(), "\n") {
		if i := strings.Index(line, "push settled"); i >= 0 {
			found = append(found, line[i:])
		}
	}
	return found
}

func captureLog(d *Daemon) *lockedLog {
	captured := &lockedLog{}
	d.log = log.New(captured, "", 0)
	return captured
}

func pendingTurns(d *Daemon, sessionID string) []string {
	d.turnPushes.mu.Lock()
	defer d.turnPushes.mu.Unlock()
	if entry := d.turnPushes.pending[sessionID]; entry != nil {
		return append([]string(nil), entry.turns...)
	}
	return nil
}

func needsYouConfirmed(d *Daemon, sessionID, turnID string) bool {
	sent, _ := d.needsYouTakesTurnPush(sessionID, turnID, func() (bool, error) { return true, nil }, func() error { return nil }, func() {})
	return sent
}

func startTurn(t *testing.T, d *Daemon, fake *daemonSurface, session *surface.Session, turnID string) {
	t.Helper()
	fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: turnID}
	d.observeSession(d.background.ctx, fake, session)
}

func endTurnWithHeadline(t *testing.T, d *Daemon, fake *daemonSurface, session *surface.Session, turnID, headline, reply string) {
	t.Helper()
	if headline != "" {
		if _, err := d.Registry.EndTurn(registry.TurnEnd{SessionID: session.ID, Headline: headline, Source: registry.TurnSourceTool}, turnID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turnID, CompletedAt: time.Now(), Reply: &surface.ReplyResult{Text: reply, Done: true}}
	d.observeSession(d.background.ctx, fake, session)
}

func runTurn(t *testing.T, d *Daemon, fake *daemonSurface, session *surface.Session, turnID, headline, reply string) {
	t.Helper()
	fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(d.background.ctx, fake, session)
	startTurn(t, d, fake, session, turnID)
	endTurnWithHeadline(t, d, fake, session, turnID, headline, reply)
}

func waitJudged(t *testing.T, d *Daemon, sessionID, turnID string) {
	t.Helper()
	eventually(t, "the judgment of "+turnID, func() bool {
		for _, j := range readJudgments(t, d, sessionID).Judgments {
			if j.TurnID == turnID && !j.JudgedAt.IsZero() {
				return true
			}
		}
		return false
	})
	d.background.wait()
}

func TestATurnEndedWithAHeadlinePushesOnceWhenItsVerdictLands(t *testing.T) {
	stub := &judgeStub{state: turnDone, hold: make(chan struct{})}
	d, r, fake, from := judgeFixture(t, stub, "")
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)

	runTurn(t, d, fake, &from, "turn-1", "Parser fixed and tests pass", "Parser fixed. Tests pass.")
	if turns := pendingTurns(d, from.ID); len(turns) != 1 || turns[0] != "turn-1" {
		t.Fatalf("the turn's push is not waiting for its verdict: %v", turns)
	}
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("the turn pushed %d times before its verdict landed", count)
	}

	close(stub.hold)
	waitJudged(t, d, from.ID, "turn-1")
	finished := pushes.all("turn.completed")
	if len(finished) != 1 || pushes.count(turnNeedsYouEvent) != 0 {
		t.Fatalf("one finished turn pushed %d finished and %d needs-you", len(finished), pushes.count(turnNeedsYouEvent))
	}
	if message := finished[0]["message"]; !strings.Contains(message, "done") || !strings.HasSuffix(message, ": Parser fixed and tests pass") || finished[0]["turnId"] != "turn-1" {
		t.Fatalf("the push %+v does not carry the turn's headline", finished[0])
	}
	if lines := settledLines(logged); len(lines) != 1 || lines[0] != "push settled session=from turns=turn-1 reason=verdict event=turn.completed injected=0" {
		t.Fatalf("settled log %q", lines)
	}
	if row := viewRow(t, d, from.ID); row.TurnCompletion == nil || row.TurnCompletion.TurnID != "turn-1" {
		t.Fatalf("the row does not record the pushed turn for its viewers: %+v", row.TurnCompletion)
	}
}

func TestATurnWithNoVerdictComingPushesOnceWhenItEnds(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)

	runTurn(t, d, fake, &from, "turn-1", "Release notes drafted", "Drafted.")
	d.background.wait()
	finished := pushes.all("turn.completed")
	if len(finished) != 1 || !strings.HasSuffix(finished[0]["message"], ": Release notes drafted") {
		t.Fatalf("a turn no judge reads pushed %+v", finished)
	}
	if lines := settledLines(logged); len(lines) != 1 || !strings.Contains(lines[0], "reason=idle") {
		t.Fatalf("settled log %q", lines)
	}
}

func TestATurnJudgedToNeedThePersonPushesOnlyNeedsYou(t *testing.T) {
	stub := &judgeStub{state: turnWaitingPerson}
	d, r, fake, from := judgeFixture(t, stub, "")
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)

	runTurn(t, d, fake, &from, "turn-1", "Fix is ready; deploy it?", "The fix is ready. Should I deploy it now?")
	waitJudged(t, d, from.ID, "turn-1")
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("a turn judged to need the person also pushed finished %d times", count)
	}
	needs := pushes.all(turnNeedsYouEvent)
	if len(needs) != 1 || !strings.Contains(needs[0]["message"], "needs you") || !strings.HasSuffix(needs[0]["message"], ": Fix is ready; deploy it?") {
		t.Fatalf("needs-you pushes %+v", needs)
	}
	if lines := settledLines(logged); len(lines) != 1 || lines[0] != "push settled session=from turns=turn-1 reason=verdict event=turn.needs_you injected=0" {
		t.Fatalf("settled log %q", lines)
	}
}

func TestATurnAQueuedMessageFollowsPushesOnceForTheLastTurn(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(d.background.ctx, fake, &from)
	startTurn(t, d, fake, &from, "turn-1")
	if err := r.QueueMessage(from.ID, "one more thing"); err != nil {
		t.Fatal(err)
	}
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("a turn with a message waiting for the session pushed %d times", count)
	}
	if r.QueueCount(from.ID) != 0 {
		t.Fatal("the queued message was not delivered when the turn ended")
	}
	startTurn(t, d, fake, &from, "turn-2")
	endTurnWithHeadline(t, d, fake, &from, "turn-2", "Follow-up handled", "Handled.")
	d.background.wait()
	finished := pushes.all("turn.completed")
	if len(finished) != 1 || finished[0]["turnId"] != "turn-2" || !strings.HasSuffix(finished[0]["message"], ": Follow-up handled") {
		t.Fatalf("two back-to-back turns pushed %+v", finished)
	}
	if lines := settledLines(logged); len(lines) != 1 || lines[0] != "push settled session=from turns=turn-1,turn-2 reason=idle event=turn.completed injected=0" {
		t.Fatalf("settled log %q", lines)
	}
}

func TestALateVerdictSendsOnlyNeedsYouInPlaceOfTheDeadlinePush(t *testing.T) {
	stub := &judgeStub{state: turnWaitingPerson, hold: make(chan struct{})}
	d, r, fake, from := judgeFixture(t, stub, "")
	d.turnPushes.window = 50 * time.Millisecond
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)

	runTurn(t, d, fake, &from, "turn-1", "Waiting on your call", "Should I ship it?")
	eventually(t, "the deadline push", func() bool { return pushes.count("turn.completed") == 1 })
	close(stub.hold)
	waitJudged(t, d, from.ID, "turn-1")
	finished, needs := pushes.all("turn.completed"), pushes.all(turnNeedsYouEvent)
	if len(finished) != 1 || len(needs) != 1 || finished[0]["collapseId"] == "" || finished[0]["collapseId"] != needs[0]["collapseId"] {
		t.Fatalf("a late needs-you verdict does not replace the deadline push: %+v %+v", finished, needs)
	}
	if lines := settledLines(logged); len(lines) != 2 || !strings.Contains(lines[0], "turns=turn-1 reason=deadline event=turn.completed injected=0") || !strings.Contains(lines[1], "turns=turn-1 reason=verdict event=turn.needs_you injected=0") {
		t.Fatalf("settled log %q", lines)
	}

	stub.mu.Lock()
	stub.state, stub.hold = turnDone, make(chan struct{})
	hold := stub.hold
	stub.mu.Unlock()
	runTurn(t, d, fake, &from, "turn-2", "Shipped", "Shipped.")
	eventually(t, "the second deadline push", func() bool { return pushes.count("turn.completed") == 2 })
	close(hold)
	waitJudged(t, d, from.ID, "turn-2")
	if count := pushes.count("turn.completed"); count != 2 {
		t.Fatalf("a late done verdict pushed again: %d finished pushes", count)
	}
}

func TestAVerdictLandingBeforeTheTurnsPushIsSetUpStillSettlesIt(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	notice := turnCompletedNotice{desktop: "from done", mobile: "from done: Shipped", outcome: turnOutcomeFinished}

	d.turnJudgmentQueued(from.ID, "turn-1")
	needsYouConfirmed(d, from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", notice)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 || pendingTurns(d, from.ID) != nil {
		t.Fatalf("a turn whose needs-you verdict landed first pushed finished %d times, pending %v", count, pendingTurns(d, from.ID))
	}

	d.turnJudgmentQueued(from.ID, "turn-2")
	d.turnVerdictLanded(from.ID, "turn-2")
	d.scheduleTurnPush(from.ID, "turn-2", notice)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("a turn whose done verdict landed first pushed %d times", count)
	}
}

func TestAFiredDeadlineDoesNotSendAPushPausedByANewTurn(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished})
	d.turnPushes.mu.Lock()
	entry := d.turnPushes.pending[from.ID]
	d.turnPushes.mu.Unlock()
	if entry == nil {
		t.Fatal("the push did not wait for its verdict")
	}

	d.turnPushes.mu.Lock()
	armed := entry.gen
	d.turnPushes.mu.Unlock()

	d.pauseTurnPush(from.ID)
	d.settleTurnPush(from.ID, entry, armed, settledOnDeadline)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("a deadline that fired as a new turn began sent %d pushes", count)
	}
	d.resumeTurnPush(from.ID)
	d.settleTurnPush(from.ID, entry, armed, settledOnDeadline)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 || pendingTurns(d, from.ID) == nil {
		t.Fatalf("a deadline armed before a pause settled the resumed push: %d pushes", count)
	}
}

func TestAnOlderNeedsYouVerdictLeavesTheNewerTurnWaitingForItsOwn(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	notice := turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished}
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.turnJudgmentQueued(from.ID, "turn-2")
	d.scheduleTurnPush(from.ID, "turn-2", notice)

	needsYouConfirmed(d, from.ID, "turn-1")
	d.background.wait()
	if turns := pendingTurns(d, from.ID); len(turns) != 1 || turns[0] != "turn-2" {
		t.Fatalf("an older turn's verdict settled the newer turn's push: pending %v", turns)
	}
	needsYouConfirmed(d, from.ID, "turn-2")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 || pendingTurns(d, from.ID) != nil {
		t.Fatalf("the newer turn's own needs-you did not stand for its push: %d finished pushes", count)
	}
}

func TestACompletionSeenWhileTheSessionStillReportsBusyPushesOnce(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(d.background.ctx, fake, &from)
	startTurn(t, d, fake, &from, "turn-1")
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusBusy, CompletedTurnID: "turn-1", CompletedAt: time.Now(), Reply: &surface.ReplyResult{Text: "Done.", Done: true}}
	d.observeSession(d.background.ctx, fake, &from)
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: time.Now(), Reply: &surface.ReplyResult{Text: "Done.", Done: true}}
	d.observeSession(d.background.ctx, fake, &from)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("a completion seen while busy pushed %d times", count)
	}
}

func TestAMessageDeliveredBeforeTheNextTurnIsSeenHoldsTheVerdictsPush(t *testing.T) {
	stub := &judgeStub{state: turnDone, hold: make(chan struct{})}
	d, r, fake, from := judgeFixture(t, stub, "")
	pushes := newPushRecorder(t, r)

	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(d.background.ctx, fake, &from)
	startTurn(t, d, fake, &from, "turn-1")
	if err := r.QueueMessage(from.ID, "one more thing"); err != nil {
		t.Fatal(err)
	}
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	if r.QueueCount(from.ID) != 0 {
		t.Fatal("the queued message was not delivered when the turn ended")
	}
	if runtime, _, _ := r.RuntimeState(from.ID); runtime.LastStatus != surface.StatusBusy {
		t.Fatalf("the delivery did not record the follow-up turn as running: %+v", runtime)
	}

	close(stub.hold)
	waitJudged(t, d, from.ID, "turn-1")
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("the first turn's verdict pushed %d times while its follow-up ran", count)
	}
	endTurnWithHeadline(t, d, fake, &from, "turn-2", "Follow-up handled", "Handled.")
	waitJudged(t, d, from.ID, "turn-2")
	finished := pushes.all("turn.completed")
	if len(finished) != 1 || finished[0]["turnId"] != "turn-2" || !strings.HasSuffix(finished[0]["message"], ": Follow-up handled") {
		t.Fatalf("the burst pushed %+v", finished)
	}
}

func TestAMessageSentToTheSessionHoldsItsPushUntilTheDeadline(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	d.turnPushes.window = 100 * time.Millisecond
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)
	notice := turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished}
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", notice)

	sent := d.turnPushHandoff(from.ID)
	sent(true)
	d.turnVerdictLanded(from.ID, "turn-1")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("the verdict pushed %d times while the message it was sent was starting a turn", count)
	}
	eventually(t, "the deadline push", func() bool { return pushes.count("turn.completed") == 1 })
	if lines := settledLines(logged); len(lines) != 1 || !strings.Contains(lines[0], "reason=deadline") {
		t.Fatalf("settled log %q", lines)
	}

	d.scheduleTurnPush(from.ID, "turn-2", notice)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 2 {
		t.Fatalf("a turn with nothing pending pushed %d times in all", count)
	}
	d.turnJudgmentQueued(from.ID, "turn-3")
	d.scheduleTurnPush(from.ID, "turn-3", notice)
	failed := d.turnPushHandoff(from.ID)
	failed(false)
	d.turnVerdictLanded(from.ID, "turn-3")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 3 {
		t.Fatalf("a send that was not accepted still held the push: %d pushes", count)
	}
}

func observeIdle(d *Daemon, fake *daemonSurface, session *surface.Session) {
	fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(d.background.ctx, fake, session)
}

func TestASendWhileATurnsPushWaitsHoldsItForTheFollowUp(t *testing.T) {
	stub := &judgeStub{state: turnDone, hold: make(chan struct{})}
	d, r, fake, from := judgeFixture(t, stub, "")
	pushes := newPushRecorder(t, r)
	observeIdle(d, fake, &from)
	startTurn(t, d, fake, &from, "turn-1")
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	d.turnPushHandoff(from.ID)(true)
	close(stub.hold)
	waitJudged(t, d, from.ID, "turn-1")
	d.armCompletionNotification(from.ID)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("a turn whose session was already sent its next message pushed %d times", count)
	}
	endTurnWithHeadline(t, d, fake, &from, "turn-2", "Follow-up handled", "Handled.")
	waitJudged(t, d, from.ID, "turn-2")
	if finished := pushes.all("turn.completed"); len(finished) != 1 || finished[0]["turnId"] != "turn-2" {
		t.Fatalf("the burst pushed %+v", finished)
	}
}

func TestTwoSentMessagesHoldThePushUntilBothTurnsEnd(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	observeIdle(d, fake, &from)
	startTurn(t, d, fake, &from, "turn-1")
	d.turnJudgmentQueued(from.ID, "turn-1")
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "One", "One.")
	d.turnPushHandoff(from.ID)(true)
	d.turnPushHandoff(from.ID)(true)
	d.turnVerdictLanded(from.ID, "turn-1")
	startTurn(t, d, fake, &from, "turn-2")
	endTurnWithHeadline(t, d, fake, &from, "turn-2", "Two", "Two.")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("the burst pushed %d times before its last turn", count)
	}
	startTurn(t, d, fake, &from, "turn-3")
	endTurnWithHeadline(t, d, fake, &from, "turn-3", "Three", "Three.")
	d.background.wait()
	if finished := pushes.all("turn.completed"); len(finished) != 1 || finished[0]["turnId"] != "turn-3" {
		t.Fatalf("the burst pushed %+v", finished)
	}
}

func TestATurnASendStartedThatWasNeverSeenRunningPushesAtOnce(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	observeIdle(d, fake, &from)
	d.turnPushHandoff(from.ID)(true)
	d.armCompletionNotification(from.ID)
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "Quick answer", "Done.")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("a quick turn a send started pushed %d times", count)
	}
}

func TestARefusedSendDoesNotReleaseAnotherAcceptedOne(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	notice := turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished}
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", notice)

	accepted, refused := d.turnPushHandoff(from.ID), d.turnPushHandoff(from.ID)
	d.turnVerdictLanded(from.ID, "turn-1")
	accepted(true)
	refused(false)
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("a refused send released the push an accepted one held: %d pushes", count)
	}
	d.turnEndedForPush(from.ID)
	d.scheduleTurnPush(from.ID, "turn-2", notice)
	d.background.wait()
	if finished := pushes.all("turn.completed"); len(finished) != 1 || finished[0]["turnId"] != "turn-2" {
		t.Fatalf("the burst pushed %+v", finished)
	}
}

func TestAnUnarmedFollowUpLetsTheEarlierTurnsPushGoAtOnce(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	observeIdle(d, fake, &from)
	startTurn(t, d, fake, &from, "turn-1")
	d.turnJudgmentQueued(from.ID, "turn-1")
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "First pass done", "Done.")
	d.turnPushHandoff(from.ID)(true)
	d.turnVerdictLanded(from.ID, "turn-1")
	endTurnWithHeadline(t, d, fake, &from, "turn-2", "Unannounced", "Done.")
	d.background.wait()
	if finished := pushes.all("turn.completed"); len(finished) != 1 || finished[0]["turnId"] != "turn-1" {
		t.Fatalf("a follow-up with no push of its own left %+v", finished)
	}
}

func TestAStoppingDaemonSendsThePushItStillHolds(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	logged := captureLog(d)
	pushes := newPushRecorder(t, r)
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished})
	d.turnPushHandoff(from.ID)(true)
	d.pauseTurnPush(from.ID)

	d.flushTurnPushes()
	d.background.stop()
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("a stopping daemon sent its held push %d times", count)
	}
	if lines := settledLines(logged); len(lines) != 1 || !strings.Contains(lines[0], "reason=shutdown") {
		t.Fatalf("settled log %q", lines)
	}
}

func TestASettledPushCountsMessagesInjectedMidTurn(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	logged := captureLog(d)
	newPushRecorder(t, r)
	observeIdle(d, fake, &from)
	d.turnPushHandoff(from.ID)(true)
	startTurn(t, d, fake, &from, "turn-1")
	d.turnPushHandoff(from.ID)(true)
	d.turnPushHandoff(from.ID)(false)
	endTurnWithHeadline(t, d, fake, &from, "turn-1", "Done", "Done.")
	d.background.wait()
	if lines := settledLines(logged); len(lines) != 1 || !strings.HasSuffix(lines[0], "injected=1") {
		t.Fatalf("a send to the idle session and one into its running turn logged %q", lines)
	}
}

func TestANeedsYouVerdictAfterTheShutdownFlushIsNotSent(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished})
	d.flushTurnPushes()
	if needsYouConfirmed(d, from.ID, "turn-1") {
		t.Fatal("a needs-you verdict for a turn the shutdown flush sent would still be pushed")
	}
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("the flush sent %d pushes", count)
	}
}

func TestAFailedNeedsYouConfirmLeavesTheFinishedPushWaiting(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	d.turnJudgmentQueued(from.ID, "turn-1")
	d.scheduleTurnPush(from.ID, "turn-1", turnCompletedNotice{desktop: "from done", mobile: "from done", outcome: turnOutcomeFinished})
	sent, _ := d.needsYouTakesTurnPush(from.ID, "turn-1", func() (bool, error) { return false, nil }, func() error { return nil }, func() { t.Fatal("an unconfirmed needs-you push was sent") })
	if sent || pendingTurns(d, from.ID) == nil {
		t.Fatalf("a failed confirm settled the turn: sent=%v pending=%v", sent, pendingTurns(d, from.ID))
	}
	d.turnVerdictLanded(from.ID, "turn-1")
	d.background.wait()
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("the finished push went out %d times after a failed needs-you confirm", count)
	}
}
