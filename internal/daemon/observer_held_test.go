package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func pushNotifications(t *testing.T, r *registry.Registry) <-chan map[string]string {
	t.Helper()
	pairing, err := r.CreateDevicePairing("Phone", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := r.CompleteDevicePairing(pairing.Secret, "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		received <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	return received
}

func expectNoNotification(t *testing.T, received <-chan map[string]string, why string) {
	t.Helper()
	select {
	case payload := <-received:
		t.Fatalf("%s: %+v", why, payload)
	case <-time.After(250 * time.Millisecond):
	}
}

func expectNotification(t *testing.T, received <-chan map[string]string, turnID string) {
	t.Helper()
	select {
	case payload := <-received:
		if payload["turnId"] != turnID {
			t.Fatalf("payload=%+v want turn %s", payload, turnID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("turn %s did not notify", turnID)
	}
}

func watched(t *testing.T, r *registry.Registry, id string) bool {
	t.Helper()
	sessions, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == id {
			return true
		}
	}
	return false
}

func TestATurnHeldByBackgroundWorkNotifiesOnceWhenThatWorkStops(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
	received := pushNotifications(t, r)
	running := &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceTranscript, Tasks: []surface.BackgroundTask{{ID: "call-build", Kind: surface.BackgroundShell, Description: "make build"}}}
	finished := &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Done: true}, BackgroundTasks: running}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(t.Context(), fake, &from)
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	d.observeSession(t.Context(), fake, &from)
	fake.observations["from"] = finished
	d.observeSession(t.Context(), fake, &from)
	d.observeSession(t.Context(), fake, &from)
	expectNoNotification(t, received, "a turn whose background command still runs notified")
	if state, _, err := r.RuntimeState("from"); err != nil || state.HeldCompletionTurnID != "turn-1" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if !watched(t, r, "from") {
		t.Fatal("a session holding a completion is not observed")
	}
	unloaded := *finished
	unloaded.BackgroundTasks = nil
	fake.observations["from"] = &unloaded
	d.observeSession(t.Context(), fake, &from)
	expectNotification(t, received, "turn-1")
	d.observeSession(t.Context(), fake, &from)
	expectNoNotification(t, received, "a released completion notified twice")
	if state, _, err := r.RuntimeState("from"); err != nil || state.HeldCompletionTurnID != "" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestATurnFinishedRelayHeldByBackgroundWorkDeliversOnceWhenThatWorkStops(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, to := daemonFixture(t)
	if err := r.PrepareRelaySource(context.Background(), fake, &from, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", false, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	running := &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceTranscript, Tasks: []surface.BackgroundTask{{ID: "call-build", Kind: surface.BackgroundShell}}}
	observe := func(background *surface.BackgroundTasks) {
		fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Text: "built", Done: true}, BackgroundTasks: background}
		d.syncRelays(context.Background(), fake, &from)
	}
	observe(running)
	observe(running)
	if count := r.QueueCount(to.ID); count != 0 {
		t.Fatalf("relayed %d while background work ran", count)
	}
	observe(nil)
	observe(nil)
	if count := r.QueueCount(to.ID); count != 1 {
		t.Fatalf("relayed %d once the background work stopped", count)
	}
}

type backgroundSourceSurface struct {
	*daemonSurface
	background *surface.BackgroundTasks
}

func (s backgroundSourceSurface) Background(context.Context, surface.Session) *surface.BackgroundTasks {
	return s.background
}

func TestAHeldTurnCompletesOnceWhenItsRuntimeCanNoLongerBeObserved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, to := daemonFixture(t)
	received := pushNotifications(t, r)
	if err := r.PrepareRelaySource(context.Background(), fake, &from, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", false, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	running := &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceTranscript, Tasks: []surface.BackgroundTask{{ID: "call-build", Kind: surface.BackgroundShell}}}
	source := backgroundSourceSurface{daemonSurface: fake, background: running}
	for _, observation := range []*surface.TurnObservation{
		{Status: surface.StatusIdle},
		{Status: surface.StatusBusy, ActiveTurnID: "turn-1"},
		{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Text: "built", Done: true}, BackgroundTasks: running},
	} {
		fake.observations["from"] = observation
		d.observeSession(t.Context(), source, &from)
	}
	fake.observeErr = errors.New("Codex is unavailable")
	d.observeSession(t.Context(), source, &from)
	expectNoNotification(t, received, "a held turn whose work may still run notified")
	if count := r.QueueCount(to.ID); count != 0 {
		t.Fatalf("relayed %d while background work may still run", count)
	}
	source.background = nil
	d.observeSession(t.Context(), source, &from)
	d.observeSession(t.Context(), source, &from)
	expectNotification(t, received, "turn-1")
	expectNoNotification(t, received, "an unobserved release notified twice")
	if state, _, err := r.RuntimeState("from"); err != nil || state.HeldCompletionTurnID != "" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if count := r.QueueCount(to.ID); count != 1 {
		t.Fatalf("relayed %d once nothing could run", count)
	}
}

func TestALaterTurnTakesOverAHeldCompletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
	received := pushNotifications(t, r)
	running := &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceTranscript, Tasks: []surface.BackgroundTask{{ID: "call-build", Kind: surface.BackgroundShell}}}
	for _, observation := range []*surface.TurnObservation{
		{Status: surface.StatusIdle},
		{Status: surface.StatusBusy, ActiveTurnID: "turn-1"},
		{Status: surface.StatusIdle, CompletedTurnID: "turn-1", Reply: &surface.ReplyResult{Done: true}, BackgroundTasks: running},
		{Status: surface.StatusBusy, ActiveTurnID: "turn-2", CompletedTurnID: "turn-1", BackgroundTasks: running},
	} {
		fake.observations["from"] = observation
		d.observeSession(t.Context(), fake, &from)
	}
	if state, _, err := r.RuntimeState("from"); err != nil || state.HeldCompletionTurnID != "" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	fake.observations["from"] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-2", Reply: &surface.ReplyResult{Done: true}}
	d.observeSession(t.Context(), fake, &from)
	expectNotification(t, received, "turn-2")
	expectNoNotification(t, received, "the superseded turn also notified")
}
