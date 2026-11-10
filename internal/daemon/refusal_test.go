package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARefusalThatReturnsAfterItClearedAsksTheSenderAgain(t *testing.T) {
	refused := surface.DeliveryRefused(errors.New("Claude refused to resume target: Workspace not trusted"))
	for _, tc := range []struct {
		name    string
		between func(t *testing.T, store *registry.Registry, id int64, attempt func(error))
	}{
		{"after an ordinary deferral", func(t *testing.T, _ *registry.Registry, _ int64, attempt func(error)) {
			attempt(surface.DeliveryUnavailable(errors.New("Claude did not resume target: Claude did not start")))
		}},
		{"after a failure and a retry", func(t *testing.T, store *registry.Registry, id int64, attempt func(error)) {
			attempt(surface.DeliveryTerminal(errors.New("target rejected the message"), surface.DeliveryInvalidRequest))
			if err := store.RetryMessage(id); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			store, err := registry.Open(registrytest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			target := surface.Session{ID: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}
			sender := surface.Session{ID: "sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
			for _, session := range []surface.Session{target, sender} {
				if err := store.RegisterSession(session); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveRuntimeState(target.ID, surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
				t.Fatal(err)
			}
			id, _, err := store.QueueDeliveryWithIntent(target.ID, "hello", "", surface.SendOptions{SourceSessionID: sender.ID})
			if err != nil {
				t.Fatal(err)
			}
			d := newTestDaemon(t, store, nil)
			now := time.Now()
			attempt := func(cause error) {
				t.Helper()
				now = now.Add(time.Hour)
				item, err := store.ClaimNextMessage(target.ID, now)
				if err != nil || item == nil || item.ID != id {
					t.Fatalf("claim=%+v err=%v, want %d", item, err, id)
				}
				d.finishQueueFailure(item, &target, cause, now, registry.SendTrace{})
			}
			notices := func() int {
				t.Helper()
				rows, err := store.ListQueue(false, nil)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, row := range rows {
					if row.SessionID == sender.ID && strings.Contains(row.Message, "Workspace not trusted") {
						count++
					}
				}
				return count
			}
			attempt(refused)
			if got := notices(); got != 1 {
				t.Fatalf("sender notices=%d after the first refusal, want 1", got)
			}
			tc.between(t, store, id, attempt)
			attempt(refused)
			if got := notices(); got != 2 {
				t.Fatalf("sender notices=%d after the refusal returned, want 2", got)
			}
			attempt(refused)
			if got := notices(); got != 2 {
				t.Fatalf("sender notices=%d after the same refusal again, want still 2", got)
			}
			if asks := openDeliveryAsks(t, store); len(asks) != 1 || asks[0].SessionID != sender.ID || asks[0].RoutedTo != sender.ID {
				t.Fatalf("asks=%+v, want one open ask for the sender", asks)
			}
		})
	}
}
