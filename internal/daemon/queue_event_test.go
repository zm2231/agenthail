package daemon

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func queueEvents(d *Daemon, sessionID string) int {
	d.events.mu.Lock()
	defer d.events.mu.Unlock()
	n := 0
	for _, event := range d.events.history {
		var data map[string]string
		if event.Type == "state.changed" && event.EntityID == sessionID && json.Unmarshal(event.Data, &data) == nil && data["source"] == "queue" {
			n++
		}
	}
	return n
}

type peerRoutedSurface struct {
	*daemonSurface
	route surface.Route
}

func (p *peerRoutedSurface) Route(*surface.Session, surface.Author) surface.Route { return p.route }

func TestAQueuedMessageHeldForABusySessionTellsClientsOnlyWhenItsQueueChanges(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	adapter := &peerRoutedSurface{daemonSurface: fake, route: surface.RoutePeer}
	if _, err := r.QueueMessageWithOptions(to.ID, "held until idle", "queue:1", surface.SendOptions{BusyDelivery: "queue"}); err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-busy"}
	for range 3 {
		d.observeSession(t.Context(), adapter, &to)
	}
	if got := queueEvents(d, to.ID); got != 0 || len(fake.sent) != 0 || r.QueueCount(to.ID) != 1 {
		t.Fatalf("busy passes that left the held message alone: queue events=%d sent=%v queued=%d, want 0, none and 1", got, fake.sent, r.QueueCount(to.ID))
	}
	adapter.route = surface.RouteSend
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-busy"}
	d.observeSession(t.Context(), adapter, &to)
	if len(fake.sent) != 1 || r.QueueCount(to.ID) != 0 || queueEvents(d, to.ID) != 1 {
		t.Fatalf("delivering the held message at idle: sent=%v queued=%d queue events=%d, want 1, 0 and 1", fake.sent, r.QueueCount(to.ID), queueEvents(d, to.ID))
	}
}

func TestAFailedQueuedDeliveryStillTellsClients(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	fake.sendErr = errors.New("the session refused it")
	if _, err := r.QueueMessageWithOptions(to.ID, "will fail", "queue:fail", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(t.Context(), fake, &to)
	if got := queueEvents(d, to.ID); got != 1 {
		t.Fatalf("a failed delivery told clients %d times, want 1", got)
	}
}

func TestADeferredQueuedDeliveryTellsClientsOnceWithItsError(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	fake.sendErr = surface.DeliveryUnavailable(errors.New("the terminal is not reachable yet"))
	id, err := r.QueueMessageWithOptions(to.ID, "will wait", "queue:defer", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(t.Context(), fake, &to)
	item, err := r.QueueItem(id, nil)
	if err != nil || item.Status != "pending" || item.Attempts != 0 || !strings.Contains(item.LastError, "not reachable yet") {
		t.Fatalf("the deferred row: %+v err=%v", item, err)
	}
	if got := queueEvents(d, to.ID); got != 1 {
		t.Fatalf("a deferred delivery told clients %d times, want 1", got)
	}
	d.observeSession(t.Context(), fake, &to)
	if got := queueEvents(d, to.ID); got != 1 {
		t.Fatalf("a pass inside the deferral's backoff told clients again: %d events", got)
	}
}
