package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestEveryDispatchedActionDeclaresWhatItChanges(t *testing.T) {
	dispatched := map[string]bool{}
	for _, name := range dispatchedActionNames(t) {
		dispatched[name] = true
		if _, declared := actionScopes[name]; !declared {
			t.Errorf("%s is dispatched but not declared in actionScopes", name)
		}
	}
	for name := range actionScopes {
		if !dispatched[name] {
			t.Errorf("actionScopes declares %s, which nothing dispatches", name)
		}
	}
}

func eventsAfter(d *Daemon, cursor uint64) []apiEvent {
	d.events.mu.Lock()
	defer d.events.mu.Unlock()
	var events []apiEvent
	for _, event := range d.events.history {
		if event.ID > cursor {
			events = append(events, event)
		}
	}
	return events
}

func TestOnlyActionsThatChangeSomethingAnnounceIt(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	d.Surfaces = []surface.Surface{&operationSurface{daemonSurface: fake}}
	router := dashboardRouter(d)
	act := func(body string) []apiEvent {
		t.Helper()
		cursor := d.events.cursor()
		if w := serveDashboardRequest(router, http.MethodPost, "/api/action", body); w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", body, w.Code, w.Body)
		}
		return eventsAfter(d, cursor)
	}
	for range 3 {
		if events := act(`{"action":"native-queue","sessionId":"from","nativeQueue":{"queueAction":"list"}}`); len(events) != 0 {
			t.Fatalf("listing the native queue published %+v, want nothing", events)
		}
	}
	sent := act(`{"action":"send","sessionId":"from","message":"hello"}`)
	if !announced(sent, "state.changed", from.ID) || announced(sent, "state.changed", "") {
		t.Fatalf("a send published %+v, want state.changed for %s and none for everyone", sent, from.ID)
	}
	cleared := act(`{"action":"queue-clear","sessionId":"fro"}`)
	if !announced(cleared, "state.changed", from.ID) {
		t.Fatalf("clearing the queue of a session named by an id prefix published %+v, want state.changed for %s", cleared, from.ID)
	}
	edited := act(`{"action":"native-queue","sessionId":"from","nativeQueue":{"queueAction":"add","message":"next","clientUserMessageId":"queued-1"}}`)
	if !announced(edited, nativeQueueChangedEvent, from.ID) || len(edited) != 1 {
		t.Fatalf("a native queue add published %+v, want only %s for %s", edited, nativeQueueChangedEvent, from.ID)
	}
}

func announced(events []apiEvent, eventType, entityID string) bool {
	for _, event := range events {
		if event.Type == eventType && event.EntityID == entityID {
			return true
		}
	}
	return false
}

func TestACodexQueueChangeOnAStreamedSessionIsAnnouncedForThatSession(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &scriptedStreamSurface{flakySeedSurface: &flakySeedSurface{daemonSurface: fake}, script: func(_ int32, ctx context.Context, onEvent func(surface.StreamEvent)) error {
		onEvent(surface.StreamEvent{Kind: surface.StreamKindNativeQueue})
		<-ctx.Done()
		return ctx.Err()
	}}
	adapter.caps.Stream = true
	cursor := d.events.cursor()
	subscription, err := d.sources.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(subscription.Cancel)
	testkit.Eventually(t, "the queue change event", 5*time.Second, func() bool {
		return announced(eventsAfter(d, cursor), nativeQueueChangedEvent, from.ID)
	})
	for _, event := range eventsAfter(d, cursor) {
		var data map[string]string
		if event.Type == nativeQueueChangedEvent && (json.Unmarshal(event.Data, &data) != nil || data["source"] != "codex") {
			t.Fatalf("queue change event %+v, want source codex", event)
		}
	}
	for _, payload := range journalPayloads(t, reg, from.ID) {
		if payload.Kind == surface.StreamKindNativeQueue {
			t.Fatalf("the queue change reached the transcript: %+v", payload)
		}
	}
}
