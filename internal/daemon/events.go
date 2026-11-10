package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

type mutationResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *mutationResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *mutationResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *mutationResponseWriter) succeeded() bool {
	return w.status >= http.StatusOK && w.status < http.StatusMultipleChoices
}

const eventHistoryLimit = 1024

type apiEvent struct {
	ID        uint64          `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	EntityID  string          `json:"entityId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type eventHub struct {
	mu        sync.Mutex
	nextID    uint64
	history   []apiEvent
	followers map[uint64]chan struct{}
	nextSubID uint64
	store     *registry.Registry
}

// eventFollower follows the hub's history from its own cursor. Wake receives
// a signal after a publish, and the follower then reads what it has not seen,
// so a burst it is slow to take waits in the history instead of ending it.
type eventFollower struct {
	Wake   <-chan struct{}
	Cancel func()
	hub    *eventHub
	cursor uint64
}

// read returns the events after the follower's cursor and advances it past
// them. gap reports that the history no longer holds every event after the
// cursor; the follower then continues from the newest event.
func (f *eventFollower) read() (events []apiEvent, gap bool) {
	h := f.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.expired(f.cursor) {
		f.cursor = h.nextID
		return nil, true
	}
	for _, event := range h.history {
		if event.ID > f.cursor {
			events = append(events, event)
		}
	}
	f.cursor = h.nextID
	return events, false
}

func (h *eventHub) expired(cursor uint64) bool {
	return len(h.history) > 0 && cursor < h.history[0].ID-1
}

func newEventHub(store *registry.Registry) *eventHub {
	hub := &eventHub{followers: map[uint64]chan struct{}{}, store: store}
	if store == nil {
		return hub
	}
	persisted, err := store.RecentDaemonEvents(eventHistoryLimit)
	if err != nil {
		return hub
	}
	for _, event := range persisted {
		hub.history = append(hub.history, apiEvent{ID: event.ID, Type: event.Type, Timestamp: event.CreatedAt, EntityID: event.EntityID, Data: append(json.RawMessage(nil), event.Payload...)})
		if event.ID > hub.nextID {
			hub.nextID = event.ID
		}
	}
	return hub
}

func (h *eventHub) publish(eventType, entityID string, value any) (apiEvent, error) {
	payload, _ := json.Marshal(value)
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	var event apiEvent
	if h.store != nil {
		persisted, err := h.store.AppendDaemonEvent(eventType, entityID, payload, now, eventHistoryLimit)
		if err != nil {
			return apiEvent{}, fmt.Errorf("persist daemon event: %w", err)
		}
		event = apiEvent{ID: persisted.ID, Type: persisted.Type, Timestamp: persisted.CreatedAt, EntityID: persisted.EntityID, Data: append(json.RawMessage(nil), persisted.Payload...)}
		h.nextID = persisted.ID
	} else {
		h.nextID++
		event = apiEvent{ID: h.nextID, Type: eventType, Timestamp: now, EntityID: entityID, Data: payload}
	}
	h.history = append(h.history, event)
	if len(h.history) > eventHistoryLimit {
		h.history = append([]apiEvent(nil), h.history[len(h.history)-eventHistoryLimit:]...)
	}
	for _, wake := range h.followers {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return event, nil
}

// subscribe returns the retained events after after and a follower whose
// cursor starts at the newest of them. reset reports that after is older than
// the history holds, so the backlog is empty and the caller must reread.
func (h *eventHub) subscribe(after uint64) (backlog []apiEvent, follower *eventFollower, reset bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reset = after > 0 && h.expired(after)
	if !reset {
		for _, event := range h.history {
			if event.ID > after {
				backlog = append(backlog, event)
			}
		}
	}
	h.nextSubID++
	id := h.nextSubID
	wake := make(chan struct{}, 1)
	h.followers[id] = wake
	follower = &eventFollower{Wake: wake, hub: h, cursor: h.nextID}
	follower.Cancel = func() {
		h.mu.Lock()
		if existing, ok := h.followers[id]; ok {
			delete(h.followers, id)
			close(existing)
		}
		h.mu.Unlock()
	}
	return backlog, follower, reset
}

func (h *eventHub) cursor() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextID
}

func (d *Daemon) publishEvent(eventType, entityID string, value any) {
	if changesSnapshot(eventType) {
		d.invalidateDashboard()
	}
	if d.events != nil {
		if _, err := d.events.publish(eventType, entityID, value); err != nil {
			d.log.Printf("publish %s: %s", eventType, err)
			return
		}
	}
}
