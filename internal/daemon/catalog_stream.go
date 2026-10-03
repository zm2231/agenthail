package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const catalogStreamReplayLimit = 4096

type catalogHub struct {
	registry    *registry.Registry
	mu          sync.Mutex
	subscribers map[uint64]chan registry.CatalogEvent
	nextID      uint64
}

func newCatalogHub(store *registry.Registry) *catalogHub {
	return &catalogHub{registry: store, subscribers: map[uint64]chan registry.CatalogEvent{}}
}

func (h *catalogHub) publish(event registry.CatalogEvent) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	persisted, created, err := h.registry.AppendCatalogEvent(event)
	if err != nil || !created {
		return persisted, created, err
	}
	for id, subscriber := range h.subscribers {
		select {
		case subscriber <- persisted:
		default:
			delete(h.subscribers, id)
			close(subscriber)
		}
	}
	return persisted, true, nil
}

func (h *catalogHub) publishSession(state registry.CatalogSessionState, event registry.CatalogEvent) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	persisted, created, err := h.registry.RecordCatalogSession(state, event)
	if err != nil || !created {
		return persisted, created, err
	}
	for id, subscriber := range h.subscribers {
		select {
		case subscriber <- persisted:
		default:
			delete(h.subscribers, id)
			close(subscriber)
		}
	}
	return persisted, true, nil
}

func (h *catalogHub) publishSurface(state registry.CatalogSurfaceState, event registry.CatalogEvent) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	persisted, created, err := h.registry.RecordCatalogSurface(state, event)
	if err != nil || !created {
		return persisted, created, err
	}
	for id, subscriber := range h.subscribers {
		select {
		case subscriber <- persisted:
		default:
			delete(h.subscribers, id)
			close(subscriber)
		}
	}
	return persisted, true, nil
}

func (h *catalogHub) reconcileOmissions(kind surface.SurfaceKind, seen map[string]struct{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	events, err := h.registry.ReconcileCatalogOmissions(kind, seen, 2)
	if err != nil {
		return err
	}
	for _, event := range events {
		for id, subscriber := range h.subscribers {
			select {
			case subscriber <- event:
			default:
				delete(h.subscribers, id)
				close(subscriber)
			}
		}
	}
	return nil
}

func (h *catalogHub) subscribe(after uint64) (registry.CatalogEventWindow, <-chan registry.CatalogEvent, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	window, err := h.registry.CatalogEventsAfter(after, catalogStreamReplayLimit)
	if err != nil {
		return registry.CatalogEventWindow{}, nil, nil, err
	}
	h.nextID++
	id := h.nextID
	stream := make(chan registry.CatalogEvent, 64)
	h.subscribers[id] = stream
	cancel := func() {
		h.mu.Lock()
		if existing, found := h.subscribers[id]; found {
			delete(h.subscribers, id)
			close(existing)
		}
		h.mu.Unlock()
	}
	return window, stream, cancel, nil
}

type catalogStreamEnvelope struct {
	Stream string          `json:"stream"`
	Seq    uint64          `json:"seq"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
}

func (d *Daemon) apiCatalogStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	after, err := parseCatalogStreamCursor(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The catalog stream cursor is invalid.")
		return
	}
	window, events, cancel, err := d.catalog.subscribe(after)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "stream_unavailable", "The catalog journal could not be read.")
		return
	}
	defer cancel()
	if window.Gap {
		writeDashboardJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "stream_gap", "message": "The catalog stream cursor is no longer retained."}, "hostEpoch": window.HostEpoch, "earliestSeq": window.EarliestSeq, "latestSeq": window.LatestSeq})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	for _, event := range window.Events {
		if !d.apiEventStreamAuthorized(r) || writeCatalogStreamEntry(w, event) != nil {
			return
		}
	}
	flusher.Flush()
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open || !d.apiEventStreamAuthorized(r) || writeCatalogStreamEntry(w, event) != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func parseCatalogStreamCursor(r *http.Request) (uint64, error) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func writeCatalogStreamEntry(w http.ResponseWriter, event registry.CatalogEvent) error {
	if !json.Valid(event.Payload) {
		return fmt.Errorf("invalid catalog event payload")
	}
	payload, err := json.Marshal(catalogStreamEnvelope{Stream: "catalog", Seq: event.Seq, Type: event.Type, Data: event.Payload})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, payload)
	return err
}

func (d *Daemon) discoverCatalog(ctx context.Context) {
	for _, adapter := range d.Surfaces {
		operationCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		sessions, err := adapter.List(operationCtx)
		cancel()
		if err != nil {
			observedAt := time.Now().UTC()
			payload, _ := json.Marshal(map[string]string{"surface": string(adapter.Name()), "health": "unavailable", "detail": "catalog discovery failed", "observedAt": observedAt.Format(time.RFC3339Nano)})
			_, _, _ = d.catalog.publishSurface(registry.CatalogSurfaceState{Surface: adapter.Name(), Health: "unavailable", Detail: "catalog discovery failed", ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: "surface.health:" + string(adapter.Name()) + ":unavailable", Type: "surface.health", EntityID: string(adapter.Name()), Payload: payload})
			continue
		}
		seen := make(map[string]struct{}, len(sessions))
		for _, session := range sessions {
			seen[session.ID] = struct{}{}
			identityCtx, identityCancel := context.WithTimeout(ctx, 3*time.Second)
			identity := catalogIdentityForSession(identityCtx, session)
			identityCancel()
			hostProject, err := json.Marshal(identity.HostProject)
			if err != nil {
				continue
			}
			checkout, err := json.Marshal(identity.Checkout)
			if err != nil {
				continue
			}
			observedAt := time.Now().UTC()
			row := d.catalogSessionRow(ctx, adapter, session, identity, observedAt)
			payload, err := json.Marshal(map[string]any{"session": row})
			if err != nil {
				continue
			}
			fingerprint, err := json.Marshal(map[string]any{"session": session, "hostProject": identity.HostProject, "checkout": identity.Checkout, "unavailableReason": identity.UnavailableReason})
			if err != nil {
				continue
			}
			key := fmt.Sprintf("session.upserted:%s:%x", session.ID, fingerprint)
			_, _, _ = d.catalog.publishSession(registry.CatalogSessionState{Session: session, HostProject: hostProject, Checkout: checkout, UnavailableReason: identity.UnavailableReason, ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: key, Type: "session.upserted", EntityID: session.ID, Payload: payload})
		}
		_ = d.catalog.reconcileOmissions(adapter.Name(), seen)
		observedAt := time.Now().UTC()
		payload, _ := json.Marshal(map[string]string{"surface": string(adapter.Name()), "health": "healthy", "observedAt": observedAt.Format(time.RFC3339Nano)})
		_, _, _ = d.catalog.publishSurface(registry.CatalogSurfaceState{Surface: adapter.Name(), Health: "healthy", ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: "surface.health:" + string(adapter.Name()) + ":healthy", Type: "surface.health", EntityID: string(adapter.Name()), Payload: payload})
	}
}

func (d *Daemon) catalogSessionRow(ctx context.Context, adapter surface.Surface, session surface.Session, identity catalogIdentity, observedAt time.Time) dashboardSession {
	alias, _ := d.Registry.ReverseAlias(session.ID)
	open := session.Surface == surface.KindClaude && claudeProcessOpen(ctx, session.PID)
	config, err := LoadDashboardConfig()
	if err != nil {
		config = DashboardConfig{}
	}
	current, reason := dashboardSessionPresence(session, d.Registry.QueueCount(session.ID), open, config.CodexRecentHours, observedAt)
	effective := surface.EffectiveCapabilities(&session, adapter.Capabilities())
	return dashboardSession{ID: session.ID, Surface: session.Surface, Name: session.Name, Cwd: session.Cwd, Alias: alias, Status: session.Status, LastActive: session.LastActive, QueueCount: d.Registry.QueueCount(session.ID), Open: open, Current: current, CurrentReason: reason, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: session.Source, Transport: session.Transport, HostProject: &identity.HostProject, Checkout: &identity.Checkout, ObservedAt: observedAt, UnavailableReason: identity.UnavailableReason}
}
