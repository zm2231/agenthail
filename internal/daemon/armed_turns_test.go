package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func runArmedTurn(t *testing.T, kind surface.SurfaceKind, runtime surface.RuntimeKind, endedAt time.Time) (*extensionFixture, <-chan map[string]string) {
	t.Helper()
	f := startExtensionFixture(t)
	r := f.d.Registry
	received := pushNotifications(t, r)
	f.fake.kind = kind
	builder := surface.Session{ID: "builder", Surface: kind, RuntimeKind: runtime, Status: surface.StatusIdle, Cwd: f.workdir}
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
	f.fake.observations[builder.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	f.d.scanAndRelay(t.Context())
	f.handle(t, extensionTurnEvent{kind: extensionTurnIdle, sessionID: "unrelated"})
	if err := os.WriteFile(filepath.Join(f.workdir, "ui.html"), []byte("<p>hello</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.fake.observations[builder.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", CompletedAt: endedAt, Reply: &surface.ReplyResult{Done: true, Text: "Built it."}}
	f.d.scanAndRelay(t.Context())
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
	if state, _, err := r.RuntimeState(builder.ID); err != nil || state.CompletedTurnID != "turn-1" || state.LastStatus != surface.StatusIdle {
		t.Fatalf("the turn's end was not recorded: %+v %v", state, err)
	}
	if watched(t, r, builder.ID) {
		t.Fatal("a session whose turn ended is still watched")
	}
	return f, received
}

func TestATurnTheDaemonStartedIsObservedToItsEndWithoutARelay(t *testing.T) {
	for kind, runtime := range map[surface.SurfaceKind]surface.RuntimeKind{surface.KindCodex: surface.RuntimeCodexAppServer, surface.KindClaude: surface.RuntimeClaudeHost} {
		t.Run(string(kind), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			_, received := runArmedTurn(t, kind, runtime, time.Now())
			expectNotification(t, received, "turn-1")
		})
	}
}

func TestATurnSeenEndingLongAfterItEndedIsRecordedWithoutNotifying(t *testing.T) {
	for kind, runtime := range map[surface.SurfaceKind]surface.RuntimeKind{surface.KindCodex: surface.RuntimeCodexAppServer, surface.KindClaude: surface.RuntimeClaudeHost} {
		t.Run(string(kind), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			_, received := runArmedTurn(t, kind, runtime, time.Now().Add(-time.Hour))
			expectNoNotification(t, received, "a turn that ended an hour before it was seen notified")
		})
	}
}
