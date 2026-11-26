package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func startHostedWorker(t *testing.T) (*extensionFixture, <-chan map[string]string) {
	t.Helper()
	return startWorker(t, surface.KindClaude, surface.RuntimeClaudeHost)
}

func startWorker(t *testing.T, kind surface.SurfaceKind, runtime surface.RuntimeKind) (*extensionFixture, <-chan map[string]string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := startExtensionFixture(t)
	received := pushNotifications(t, f.d.Registry)
	f.fake.kind = kind
	worker := surface.Session{ID: "worker", Surface: kind, RuntimeKind: runtime, Status: surface.StatusIdle, Cwd: f.workdir}
	f.fake.sessions[worker.ID] = worker
	if err := f.d.Registry.RecordObservedSession(worker); err != nil {
		t.Fatal(err)
	}
	return f, received
}

func deliveredTurnRuns(t *testing.T, f *extensionFixture, turnID string) {
	t.Helper()
	if err := f.d.Registry.MarkDeliveryStarted("worker", "delivery-"+turnID, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Registry.OpenTurn("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: turnID}
	f.d.scanAndRelay(t.Context())
}

func turnEndsWith(t *testing.T, f *extensionFixture, turnID, headline string) {
	t.Helper()
	if _, err := f.d.Registry.EndTurn(registry.TurnEnd{SessionID: "worker", Headline: headline, Source: registry.TurnSourceTool}, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turnID, CompletedAt: time.Now(), Reply: &surface.ReplyResult{Done: true, Text: headline + "."}}
}

func endBoundTo(t *testing.T, f *extensionFixture, turnID, headline string) {
	t.Helper()
	end, ok, err := f.d.Registry.CompletedTurnEnd("worker", turnID)
	if err != nil || !ok || end.Headline != headline {
		t.Fatalf("the end of %s is not bound to its completion: %+v %v %v", turnID, end, ok, err)
	}
}

func TestATurnAHostedSessionStartsItselfIsObservedToItsEnd(t *testing.T) {
	f, received := startHostedWorker(t)
	deliveredTurnRuns(t, f, "turn-1")
	turnEndsWith(t, f, "turn-1", "Started the build")
	f.d.scanAndRelay(t.Context())
	expectNotification(t, received, "turn-1")

	if err := f.d.Registry.OpenTurn("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-2"}
	f.d.scanAndRelay(t.Context())
	turnEndsWith(t, f, "turn-2", "Checked the build")
	f.d.scanAndRelay(t.Context())

	state, _, err := f.d.Registry.RuntimeState("worker")
	if err != nil || state.CompletedTurnID != "turn-2" {
		t.Fatalf("a turn the session started itself was not seen ending: %+v %v", state, err)
	}
	endBoundTo(t, f, "turn-2", "Checked the build")
	expectNoNotification(t, received, "a turn the session started itself pushed")
}

func TestATurnEndSeenAfterTheNextTurnOpensStaysWithItsOwnTurn(t *testing.T) {
	f, received := startHostedWorker(t)
	deliveredTurnRuns(t, f, "turn-1")
	turnEndsWith(t, f, "turn-1", "Started the build")
	if err := f.d.Registry.OpenTurn("worker", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	f.d.scanAndRelay(t.Context())
	expectNotification(t, received, "turn-1")
	endBoundTo(t, f, "turn-1", "Started the build")
}

func TestACodexTurnFirstSeenEndedIsBoundThoughItsTimeIsInWholeSeconds(t *testing.T) {
	f, received := startWorker(t, surface.KindCodex, surface.RuntimeCodexDesktop)
	opened := time.Now().Truncate(time.Second).Add(500 * time.Millisecond)
	if err := f.d.Registry.OpenTurn("worker", opened); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Registry.EndTurn(registry.TurnEnd{SessionID: "worker", Headline: "Checked the build", Source: registry.TurnSourceTool}, "", opened.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: opened.Truncate(time.Second), Reply: &surface.ReplyResult{Done: true, Text: "Checked the build."}}
	f.d.scanAndRelay(t.Context())
	endBoundTo(t, f, "turn-1", "Checked the build")
	expectNoNotification(t, received, "a turn the session started itself pushed")
}

func TestACodexCompletionInTheSecondTheNextTurnOpenedStaysWithItsOwnTurn(t *testing.T) {
	f, received := startWorker(t, surface.KindCodex, surface.RuntimeCodexDesktop)
	second := time.Now().Truncate(time.Second)
	if err := f.d.Registry.OpenTurn("worker", second.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Registry.EndTurn(registry.TurnEnd{SessionID: "worker", Headline: "Started the build", Source: registry.TurnSourceTool}, "", second.Add(200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := f.d.Registry.OpenTurn("worker", second.Add(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: second, Reply: &surface.ReplyResult{Done: true, Text: "Started the build."}}
	f.d.scanAndRelay(t.Context())
	endBoundTo(t, f, "turn-1", "Started the build")
	expectNoNotification(t, received, "a turn the session started itself pushed")
}

func TestATurnOfAClaudeSessionWhoseProcessExitedIsStillSeenEnding(t *testing.T) {
	f, received := startHostedWorker(t)
	worker := f.fake.sessions["worker"]
	worker.RuntimeKind, worker.PID = surface.RuntimeClaudeCLI, exitedPID(t)
	f.fake.sessions["worker"] = worker
	if err := f.d.Registry.RecordObservedSession(worker); err != nil {
		t.Fatal(err)
	}
	opened := time.Now().Add(-time.Minute)
	if err := f.d.Registry.OpenTurn("worker", opened); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Registry.EndTurn(registry.TurnEnd{SessionID: "worker", Headline: "Wrote the report", Source: registry.TurnSourceTool}, "", opened.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: opened.Add(2 * time.Second), Reply: &surface.ReplyResult{Done: true, Text: "Wrote the report."}}
	f.d.scanAndRelay(t.Context())
	endBoundTo(t, f, "turn-1", "Wrote the report")
	expectNoNotification(t, received, "a turn the session started itself pushed")
}

func workerStoppedMidTurn(t *testing.T, f *extensionFixture) {
	t.Helper()
	worker := f.fake.sessions["worker"]
	worker.Status, worker.Ended = surface.StatusOffline, true
	f.fake.sessions["worker"] = worker
	if err := f.d.Registry.RecordObservedSession(worker); err != nil {
		t.Fatal(err)
	}
	f.fake.observations["worker"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: time.Now().Add(-time.Minute), Reply: &surface.ReplyResult{Done: true, Text: "Started the build."}}
}

func expectNotWatched(t *testing.T, f *extensionFixture) {
	t.Helper()
	watched, err := f.d.Registry.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range watched {
		if session.ID == "worker" {
			t.Fatalf("a session stopped mid-turn is still watched: %+v", session)
		}
	}
}

func TestATurnThatEndedBeforeItsSessionWasStoppedIsNoLongerWatched(t *testing.T) {
	f, _ := startHostedWorker(t)
	deliveredTurnRuns(t, f, "turn-1")
	turnEndsWith(t, f, "turn-1", "Started the build")
	f.d.scanAndRelay(t.Context())
	if err := f.d.Registry.OpenTurn("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Registry.EndTurn(registry.TurnEnd{SessionID: "worker", Headline: "Opened the pull request", Source: registry.TurnSourceTool}, "turn-2", time.Now()); err != nil {
		t.Fatal(err)
	}
	workerStoppedMidTurn(t, f)
	f.d.scanAndRelay(t.Context())
	expectNotWatched(t, f)
	end, ok, err := f.d.Registry.CurrentTurnEnd("worker")
	if err != nil || !ok || end.Headline != "Opened the pull request" {
		t.Fatalf("the last end of a stopped session no longer stands: %+v %v %v", end, ok, err)
	}
}

func TestATurnOpenedByAPromptWhenItsSessionWasStoppedIsNoLongerWatched(t *testing.T) {
	f, _ := startHostedWorker(t)
	deliveredTurnRuns(t, f, "turn-1")
	turnEndsWith(t, f, "turn-1", "Started the build")
	f.d.scanAndRelay(t.Context())
	if err := f.d.Registry.OpenTurn("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	workerStoppedMidTurn(t, f)
	f.d.scanAndRelay(t.Context())
	expectNotWatched(t, f)
}

func TestAStoppedSessionThatCannotBeObservedKeepsItsTurnUntilRetriesBackOff(t *testing.T) {
	f, _ := startHostedWorker(t)
	deliveredTurnRuns(t, f, "turn-1")
	turnEndsWith(t, f, "turn-1", "Started the build")
	f.d.scanAndRelay(t.Context())
	if err := f.d.Registry.OpenTurn("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	workerStoppedMidTurn(t, f)
	f.fake.observeErr = errors.New("transcript unreadable")
	watchedAfter := func() bool {
		f.d.retryMu.Lock()
		retry := f.d.observeRetry["worker"]
		retry.retryAt = time.Now()
		f.d.observeRetry["worker"] = retry
		f.d.retryMu.Unlock()
		f.d.scanAndRelay(t.Context())
		watched, err := f.d.Registry.WatchedSessions()
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range watched {
			if session.ID == "worker" {
				return true
			}
		}
		return false
	}
	if !watchedAfter() {
		t.Fatal("one failed observation stopped the turn")
	}
	for range 10 {
		if !watchedAfter() {
			return
		}
	}
	t.Fatal("a stopped session that cannot be observed is watched for as long as its turn stays open")
}

func TestATurnOpenedAfterAScanStartedOutlivesThatScansObservation(t *testing.T) {
	f, _ := startHostedWorker(t)
	deliveredTurnRuns(t, f, "turn-1")
	turnEndsWith(t, f, "turn-1", "Started the build")
	f.d.scanAndRelay(t.Context())
	workerStoppedMidTurn(t, f)
	scan := newRelayScan()
	time.Sleep(5 * time.Millisecond)
	if err := f.d.Registry.OpenTurn("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	worker := f.fake.sessions["worker"]
	f.d.observeInScan(t.Context(), scan, f.fake, &worker, true)
	watched, err := f.d.Registry.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range watched {
		if session.ID == "worker" {
			return
		}
	}
	t.Fatal("a turn opened after the scan's shared reads was stopped on them")
}
