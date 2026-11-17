package daemon

import (
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
