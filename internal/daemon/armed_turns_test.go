package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type armedTurn struct {
	kind           surface.SurfaceKind
	runtime        surface.RuntimeKind
	pid            int
	firstSeenEnded bool
	end            *surface.TurnObservation
	commits        bool
	notifies       bool
	completion     string
}

func runArmedTurn(t *testing.T, turn armedTurn) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := startExtensionFixture(t)
	r := f.d.Registry
	received := pushNotifications(t, r)
	f.fake.kind = turn.kind
	builder := surface.Session{ID: "builder", Surface: turn.kind, RuntimeKind: turn.runtime, Status: surface.StatusIdle, Cwd: f.workdir, PID: turn.pid}
	f.fake.sessions[builder.ID] = builder
	if err := r.RegisterSession(builder); err != nil {
		t.Fatal(err)
	}
	if watched(t, r, builder.ID) {
		t.Fatal("a session with no relay, queue or task is watched before any turn")
	}
	if err := r.MarkDeliveryStarted(builder.ID, "delivery-1", ""); err != nil {
		t.Fatal(err)
	}
	if !watched(t, r, builder.ID) {
		t.Fatal("a turn the daemon started is not watched")
	}
	if !turn.firstSeenEnded {
		f.fake.observations[builder.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
		f.d.scanAndRelay(t.Context())
		f.handle(t, extensionTurnEvent{kind: extensionTurnIdle, sessionID: "unrelated"})
	}
	if err := os.WriteFile(filepath.Join(f.workdir, "ui.html"), []byte("<p>hello</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.fake.observations[builder.ID] = turn.end
	f.d.scanAndRelay(t.Context())
	f.handle(t, extensionTurnEvent{kind: extensionTurnIdle, sessionID: "unrelated"})
	if turn.commits {
		deadline := time.Now().Add(10 * time.Second)
		for {
			changes := f.changes(t)
			if len(changes) > 0 && changes[0].SessionID == builder.ID && changes[0].TurnID == "turn-1" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the builder's turn is not in the extension history: %+v", changes)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	state, _, err := r.RuntimeState(builder.ID)
	if err != nil || state.CompletedTurnID != turn.completion || state.NotificationArmed || state.HeldCompletionTurnID != "" {
		t.Fatalf("the turn was left open: %+v %v", state, err)
	}
	if watched(t, r, builder.ID) {
		t.Fatal("a session whose turn ended is still watched")
	}
	if turn.notifies {
		expectNotification(t, received, "turn-1")
	} else {
		expectNoNotification(t, received, "a turn that ended unseen notified")
	}
}

func ended(at time.Time) *surface.TurnObservation {
	return &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: at, Reply: &surface.ReplyResult{Done: true, Text: "Built it."}}
}

var armedSurfaces = map[surface.SurfaceKind]surface.RuntimeKind{surface.KindCodex: surface.RuntimeCodexAppServer, surface.KindClaude: surface.RuntimeClaudeHost}

func TestATurnTheDaemonStartedIsObservedToItsEndWithoutARelay(t *testing.T) {
	for kind, runtime := range armedSurfaces {
		t.Run(string(kind), func(t *testing.T) {
			runArmedTurn(t, armedTurn{kind: kind, runtime: runtime, end: ended(time.Now()), commits: true, notifies: true, completion: "turn-1"})
		})
	}
}

func TestATurnSeenEndingLongAfterItEndedIsRecordedWithoutNotifying(t *testing.T) {
	for kind, runtime := range armedSurfaces {
		t.Run(string(kind), func(t *testing.T) {
			runArmedTurn(t, armedTurn{kind: kind, runtime: runtime, end: ended(time.Now().Add(-time.Hour)), commits: true, completion: "turn-1"})
		})
	}
}

func TestATurnEndedAfterItsProcessExitedIsStillRecorded(t *testing.T) {
	runArmedTurn(t, armedTurn{kind: surface.KindClaude, runtime: surface.RuntimeClaudeHost, pid: exitedPID(t), firstSeenEnded: true, end: ended(time.Now()), commits: true, notifies: true, completion: "turn-1"})
}

func TestALongEndedTurnWithBackgroundWorkIsNotHeldToNotifyLater(t *testing.T) {
	stale := ended(time.Now().Add(-time.Hour))
	stale.BackgroundTasks = &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceTranscript, Tasks: []surface.BackgroundTask{{ID: "call-build", Kind: surface.BackgroundShell, Description: "make build"}}}
	runArmedTurn(t, armedTurn{kind: surface.KindCodex, runtime: surface.RuntimeCodexAppServer, end: stale, commits: true, completion: "turn-1"})
}

func TestAnOpenTurnOnAThreadNoRuntimeHoldsIsClosedWithoutNotifying(t *testing.T) {
	runArmedTurn(t, armedTurn{kind: surface.KindCodex, runtime: surface.RuntimeCodexAppServer, end: &surface.TurnObservation{Status: surface.StatusNotLoaded}})
}

func TestAnOpenTurnWhoseProcessExitedIsClosedWithoutNotifying(t *testing.T) {
	runArmedTurn(t, armedTurn{kind: surface.KindClaude, runtime: surface.RuntimeClaudeHost, pid: exitedPID(t), firstSeenEnded: true, end: &surface.TurnObservation{Status: surface.StatusUnknown}})
}
