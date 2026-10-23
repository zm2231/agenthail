package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const busyConversation = "conversation-busy"

// busyClaude is a live Claude session in the middle of a turn, reachable over
// a native peer socket or only over Remote Control.
func busyClaude(t *testing.T, socket bool) (*registry.Registry, *claudetest.Fixture, surface.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	process := claudetest.New(t)
	transport := ""
	if socket {
		process.Listen()
		transport = "uds"
	}
	transcript := process.Transcript(busyConversation, process.Home)
	appendClaudeRecords(t, transcript, `{"type":"user","uuid":"prompt","timestamp":"2026-10-19T08:14:31Z","message":{"content":"Plan the release"}}`)
	process.RecordStatus(busyConversation, process.Home, "session_busy", "busy", "", time.Now())
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := surface.Session{ID: busyConversation, Bridge: "session_busy", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.PID, Transport: transport, Status: surface.StatusBusy, HasLocal: true, Transcript: transcript}
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	return store, process, session
}

func TestRelayToABusyClaudeSessionRidesItsPeerSocketAtOnce(t *testing.T) {
	store, process, session := busyClaude(t, true)
	if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: "queue"}); err != nil {
		t.Fatal(err)
	}
	manager := claudetest.ListenPeerManager(t, process.Home)
	from := surface.Session{ID: "from", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := store.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddRoute(from.ID, session.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	runDaemonWith(t, store, func(d *Daemon) { d.fireRelays(&from, "relay-turn", 0, "finished the release notes") }, surfaces.NewClaude(nil, process.Home))
	eventually(t, "the relay over the peer socket", func() bool { return manager.Containing("finished the release notes") == 1 })
	sends := manager.Sends()
	if len(sends) != 1 || sends[0].Source != from.ID || sends[0].Target == "" {
		t.Fatalf("peer sends=%+v, want one framed from the relay source", sends)
	}
	eventually(t, "the relay acknowledged", func() bool { return store.QueueCount(session.ID) == 0 })
}

func TestQueuedMessageToABusyRemoteControlClaudeSteersUnderTheSteerPolicy(t *testing.T) {
	store, process, session := busyClaude(t, false)
	if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: "steer"}); err != nil {
		t.Fatal(err)
	}
	claude := &recordingClaude{Claude: surfaces.NewClaude(nil, process.Home)}
	id, _, err := store.QueueDeliveryWithIntent(session.ID, "into the turn", "", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runDaemon(t, store, claude)
	eventually(t, "the steer into the running turn", func() bool {
		_, steered := claude.delivered()
		return len(steered) == 1 && steered[0] == "into the turn"
	})
	if sent, _ := claude.delivered(); len(sent) != 0 {
		t.Fatalf("sent=%q, want only the steer", sent)
	}
	if item, err := store.QueueItem(id, nil); err != nil || item.BusyDelivery != "" {
		t.Fatalf("item=%+v err=%v, want no per-message choice recorded", item, err)
	}
}

func TestRelayToABusyCodexSessionFollowsTheLivePolicy(t *testing.T) {
	for _, policy := range []deliverypolicy.Mode{deliverypolicy.Queue, deliverypolicy.Steer} {
		t.Run(string(policy), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: policy}); err != nil {
				t.Fatal(err)
			}
			d, r, fake, from, to := daemonFixture(t)
			fake.caps = surface.Capabilities{Send: true, Steer: true}
			fake.rejectBusy = true
			to.Status = surface.StatusBusy
			if err := r.RegisterSession(to); err != nil {
				t.Fatal(err)
			}
			fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn"}
			if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
				t.Fatal(err)
			}
			d.fireRelays(&from, "relay-turn", 0, "forwarded reply")
			d.scanAndRelay(context.Background())
			steered := len(fake.steered) == 1 && strings.Contains(fake.steered[0], "forwarded reply")
			if policy == deliverypolicy.Steer && (!steered || r.QueueCount(to.ID) != 0) {
				t.Fatalf("steered=%v queued=%d, want the relay steered", fake.steered, r.QueueCount(to.ID))
			}
			if policy == deliverypolicy.Queue && (len(fake.steered) != 0 || len(fake.sent) != 0 || r.QueueCount(to.ID) != 1) {
				t.Fatalf("steered=%v sent=%v queued=%d, want the relay held for the turn", fake.steered, fake.sent, r.QueueCount(to.ID))
			}
		})
	}
}

func TestQueuedMessagesToABusyBridgedPeerNeverSteerUnderTheSteerPolicy(t *testing.T) {
	store, process, session := busyClaude(t, true)
	if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: "steer"}); err != nil {
		t.Fatal(err)
	}
	claude := &recordingClaude{Claude: surfaces.NewClaude(nil, process.Home)}
	ids := []int64{}
	for _, message := range []string{"first handoff", "second handoff"} {
		id, _, err := store.QueueDeliveryWithIntent(session.ID, message, "", surface.SendOptions{SourceSessionID: "sender"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if item, err := store.QueueItem(ids[1], surface.Adapters{claude}); err != nil || item.HeldBy != registry.QueueHeldBehind {
		t.Fatalf("item=%+v err=%v, want the second message behind the first", item, err)
	}
	runDaemon(t, store, claude)
	eventually(t, "both messages over the peer socket", func() bool {
		sent, _ := claude.delivered()
		return len(sent) == 2
	})
	if sent, steered := claude.delivered(); len(steered) != 0 || sent[0] != "first handoff" || sent[1] != "second handoff" {
		t.Fatalf("sent=%q steered=%q, want both in order over the socket", sent, steered)
	}
}
