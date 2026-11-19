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
	time.Sleep(200 * time.Millisecond)
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
	if lines := settledLines(logged); len(lines) != 1 || lines[0] != "push settled session=from turns=turn-1 reason=verdict event=turn.completed" {
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
	if lines := settledLines(logged); len(lines) != 1 || lines[0] != "push settled session=from turns=turn-1 reason=verdict event=turn.needs_you" {
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
	if lines := settledLines(logged); len(lines) != 1 || lines[0] != "push settled session=from turns=turn-1,turn-2 reason=idle event=turn.completed" {
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
	if lines := settledLines(logged); len(lines) != 1 || !strings.Contains(lines[0], "reason=deadline") {
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
