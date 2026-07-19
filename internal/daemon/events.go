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
	mu          sync.Mutex
	nextID      uint64
	history     []apiEvent
	subscribers map[uint64]chan apiEvent
	nextSubID   uint64
	store       *registry.Registry
}

func newEventHub(store *registry.Registry) *eventHub {
	hub := &eventHub{subscribers: map[uint64]chan apiEvent{}, store: store}
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
	for id, subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(h.subscribers, id)
			close(subscriber)
		}
	}
	return event, nil
}

func (h *eventHub) subscribe(after uint64) ([]apiEvent, <-chan apiEvent, bool, func()) {
	h.mu.Lock()
	reset := after > 0 && len(h.history) > 0 && after < h.history[0].ID-1
	backlog := make([]apiEvent, 0, len(h.history))
	if !reset {
		for _, event := range h.history {
			if event.ID > after {
				backlog = append(backlog, event)
			}
		}
	}
	h.nextSubID++
	id := h.nextSubID
	stream := make(chan apiEvent, 64)
	h.subscribers[id] = stream
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if existing, ok := h.subscribers[id]; ok {
			delete(h.subscribers, id)
			close(existing)
		}
		h.mu.Unlock()
	}
	return backlog, stream, reset, cancel
}

func (h *eventHub) cursor() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextID
}

func (d *Daemon) publishEvent(eventType, entityID string, value any) {
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	if d.events != nil {
		if _, err := d.events.publish(eventType, entityID, value); err != nil {
			d.log.Printf("publish %s: %s", eventType, err)
			return
		}
	}
}
