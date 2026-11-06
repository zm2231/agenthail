package daemon

import (
	"context"
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASendQueuedForAClaudeSessionWithNoProcessLeftIsDeliveredOnce(t *testing.T) {
	const id = "ended-claude-conversation"
	const message = "deliver after the session ended"
	for _, test := range []struct {
		name     string
		ended    surface.Session
		reusedBy *surface.Session
	}{
		{
			name:  "stored with no PID",
			ended: surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusOffline},
		},
		{
			name:     "PID taken by another session",
			ended:    surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: os.Getpid(), Status: surface.StatusOffline},
			reusedBy: &surface.Session{ID: "new-claude-conversation", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: os.Getpid(), Status: surface.StatusIdle},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, err := registry.Open(registrytest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { r.Close() })
			fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{id: {Status: surface.StatusIdle}}, accepted: true}
			if err := r.RegisterSession(test.ended); err != nil {
				t.Fatal(err)
			}
			if test.reusedBy != nil {
				fake.sessions[test.reusedBy.ID] = *test.reusedBy
				if err := r.RegisterSession(*test.reusedBy); err != nil {
					t.Fatal(err)
				}
				if stored, err := r.Session(id); err != nil || stored.PID != 0 {
					t.Fatalf("stored=%+v err=%v, want the reused PID cleared", stored, err)
				}
			}
			if err := r.QueueMessage(id, message); err != nil {
				t.Fatal(err)
			}

			d := newTestDaemon(t, r, []surface.Surface{fake})
			for range 2 {
				d.scanAndRelay(context.Background())
			}

			delivered := 0
			for _, sent := range fake.sent {
				if sent == message {
					delivered++
				}
			}
			if delivered != 1 || r.QueueCount(id) != 0 {
				t.Fatalf("sent=%q pending=%d", fake.sent, r.QueueCount(id))
			}
		})
	}
}

func TestALiveBackgroundClaudeSessionWithNoPIDYetIsStillObserved(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	background := surface.Session{ID: "background-conversation", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeBackground, Status: surface.StatusUnknown, Source: "agenthail"}
	listener := surface.Session{ID: "listener", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	for _, session := range []surface.Session{background, listener} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.AddRoute(background.ID, listener.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{background.ID: background}, observations: map[string]*surface.TurnObservation{background.ID: {Status: surface.StatusBusy, ActiveTurnID: "first"}}, accepted: true}

	newTestDaemon(t, r, []surface.Surface{fake}).scanAndRelay(context.Background())

	if fake.observeCalls.Load() == 0 {
		t.Fatal("a live background session with no PID yet was not observed")
	}
}
