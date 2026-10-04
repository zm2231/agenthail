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
	registry     *registry.Registry
	mu           sync.Mutex
	subscribers  map[uint64]*catalogSubscriber
	nextID       uint64
	publishedSeq uint64
}

type catalogSubscriber struct {
	stream chan registry.CatalogEvent
	seq    uint64
}

func newCatalogHub(store *registry.Registry) *catalogHub {
	_, latest, err := store.CatalogState()
	if err != nil {
		latest = 0
	}
	return &catalogHub{registry: store, subscribers: map[uint64]*catalogSubscriber{}, publishedSeq: latest}
}

func (h *catalogHub) publish(event registry.CatalogEvent) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	persisted, created, err := h.registry.AppendCatalogEvent(event)
	if err != nil {
		return persisted, created, err
	}
	if err := h.flushCommittedLocked(); err != nil {
		return persisted, created, err
	}
	return persisted, created, nil
}

func (h *catalogHub) publishSession(state registry.CatalogSessionState, event registry.CatalogEvent) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	persisted, created, err := h.registry.RecordCatalogSession(state, event)
	if err != nil {
		return persisted, created, err
	}
	if err := h.flushCommittedLocked(); err != nil {
		return persisted, created, err
	}
	return persisted, created, nil
}

func (h *catalogHub) publishSurface(state registry.CatalogSurfaceState, event registry.CatalogEvent) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	persisted, created, err := h.registry.RecordCatalogSurface(state, event)
	if err != nil {
		return persisted, created, err
	}
	if err := h.flushCommittedLocked(); err != nil {
		return persisted, created, err
	}
	return persisted, created, nil
}

func (h *catalogHub) reconcileOmissions(kind surface.SurfaceKind, seen map[string]struct{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.registry.ReconcileCatalogOmissions(kind, seen, 2)
	if err != nil {
		return err
	}
	return h.flushCommittedLocked()
}

// flushCommitted publishes journal rows only after their owning transaction
// committed. It preserves sequence order across catalog writers that do not
// route through the hub themselves.
func (h *catalogHub) flushCommitted() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.flushCommittedLocked()
}

func (h *catalogHub) flushCommittedLocked() error {
	window, err := h.registry.CatalogEventsAfter(h.publishedSeq, catalogStreamReplayLimit)
	if err != nil {
		return err
	}
	if window.Gap {
		after := h.publishedSeq
		h.closeSubscribersLocked()
		h.publishedSeq = window.LatestSeq
		return catalogJournalGapError{After: after, Earliest: window.EarliestSeq, Latest: window.LatestSeq}
	}
	for _, event := range window.Events {
		h.broadcastLocked(event)
		h.publishedSeq = event.Seq
	}
	return nil
}

type catalogJournalGapError struct {
	After    uint64
	Earliest uint64
	Latest   uint64
}

func (e catalogJournalGapError) Error() string {
	return fmt.Sprintf("catalog journal gap after sequence %d (retained %d through %d)", e.After, e.Earliest, e.Latest)
}

func (h *catalogHub) broadcastLocked(event registry.CatalogEvent) {
	for id, subscriber := range h.subscribers {
		if event.Seq <= subscriber.seq {
			continue
		}
		select {
		case subscriber.stream <- event:
			subscriber.seq = event.Seq
		default:
			delete(h.subscribers, id)
			close(subscriber.stream)
		}
	}
}

func (h *catalogHub) closeSubscribersLocked() {
	for id, subscriber := range h.subscribers {
		delete(h.subscribers, id)
		close(subscriber.stream)
	}
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
	h.subscribers[id] = &catalogSubscriber{stream: stream, seq: window.LatestSeq}
	cancel := func() {
		h.mu.Lock()
		if existing, found := h.subscribers[id]; found {
			delete(h.subscribers, id)
			close(existing.stream)
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
	config, err := LoadDashboardConfig()
	if err != nil {
		d.log.Printf("catalog config: %s", err)
		return
	}
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		d.log.Printf("catalog queue counts: %s", err)
		return
	}
	aliases, err := d.Registry.ListAliases()
	if err != nil {
		d.log.Printf("catalog aliases: %s", err)
		return
	}
	aliasByID := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		aliasByID[alias.SessionID] = alias.Name
	}
	openClaude := claudeOpenProcesses(ctx)
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
		d.correlatePendingLaunches(operationCtx, sessions)
		for _, session := range sessions {
			if session.Runtime == nil {
				session.Runtime = &surface.Runtime{Launcher: surface.LauncherExternal, Focusable: false}
			}
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
			open := session.Surface == surface.KindClaude && openClaude[session.PID]
			row := d.catalogSessionProjection(adapter, session, identity, observedAt, aliasByID[session.ID], counts[session.ID], open, config)
			payload, err := json.Marshal(map[string]any{"session": row})
			if err != nil {
				continue
			}
			projection := row
			projection.ObservedAt = time.Time{}
			fingerprint, err := json.Marshal(projection)
			if err != nil {
				continue
			}
			key := "session.upserted:" + session.ID
			_, _, _ = d.catalog.publishSession(registry.CatalogSessionState{Session: session, HostProject: hostProject, Checkout: checkout, UnavailableReason: identity.UnavailableReason, ObservedAt: observedAt, ProjectionFingerprint: string(fingerprint)}, registry.CatalogEvent{DedupeKey: key, Type: "session.upserted", EntityID: session.ID, Payload: payload})
		}
		complete := true
		if bounded, ok := adapter.(surface.CatalogListCompleteness); ok {
			complete = bounded.CatalogListComplete()
		}
		if complete {
			_ = d.catalog.reconcileOmissions(adapter.Name(), seen)
		}
		observedAt := time.Now().UTC()
		payload, _ := json.Marshal(map[string]string{"surface": string(adapter.Name()), "health": "healthy", "observedAt": observedAt.Format(time.RFC3339Nano)})
		_, _, _ = d.catalog.publishSurface(registry.CatalogSurfaceState{Surface: adapter.Name(), Health: "healthy", ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: "surface.health:" + string(adapter.Name()) + ":healthy", Type: "surface.health", EntityID: string(adapter.Name()), Payload: payload})
	}
}

func (d *Daemon) correlatePendingLaunches(ctx context.Context, sessions []surface.Session) {
	if d.transportResolver == nil {
		return
	}
	pending, err := d.Registry.PendingLaunches()
	if err != nil {
		return
	}
	for _, item := range pending {
		launcher, ok := d.transportResolver.Launcher(item.Launcher)
		if !ok {
			continue
		}
		locations := launcher.Locate(ctx, sessions)
		matched := ""
		for id, location := range locations {
			if !locationMatches(item.Location, location) {
				continue
			}
			if matched != "" {
				matched = ""
				break
			}
			matched = id
		}
		if matched == "" {
			continue
		}
		for index := range sessions {
			if sessions[index].ID == matched {
				located := locations[matched]
				locationsCopy := located
				sessions[index].Runtime = &surface.Runtime{Launcher: item.Launcher, Location: &locationsCopy, Focusable: true}
				_ = d.Registry.RegisterSession(sessions[index])
				_ = d.Registry.DeletePendingLaunch(item.ID)
				break
			}
		}
	}
}

func (d *Daemon) catalogSessionRow(ctx context.Context, adapter surface.Surface, session surface.Session, identity catalogIdentity, observedAt time.Time) dashboardSession {
	alias, _ := d.Registry.ReverseAlias(session.ID)
	open := session.Surface == surface.KindClaude && claudeProcessOpen(ctx, session.PID)
	config, err := LoadDashboardConfig()
	if err != nil {
		config = DashboardConfig{}
	}
	return d.catalogSessionProjection(adapter, session, identity, observedAt, alias, d.Registry.QueueCount(session.ID), open, config)
}

func (d *Daemon) catalogSessionProjection(adapter surface.Surface, session surface.Session, identity catalogIdentity, observedAt time.Time, alias string, queueCount int, open bool, config DashboardConfig) dashboardSession {
	if session.Runtime == nil {
		session.Runtime = &surface.Runtime{Launcher: surface.LauncherExternal, Focusable: false}
	}
	current, reason := dashboardSessionPresence(session, queueCount, open, config.CodexRecentHours, observedAt)
	effective := surface.EffectiveCapabilities(&session, adapter.Capabilities())
	return dashboardSession{ID: session.ID, Surface: session.Surface, Name: session.Name, Cwd: session.Cwd, Alias: alias, Status: session.Status, LastActive: session.LastActive, QueueCount: queueCount, Open: open, Current: current, CurrentReason: reason, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: session.Source, Transport: session.Transport, HostProject: &identity.HostProject, Checkout: &identity.Checkout, ObservedAt: observedAt, UnavailableReason: identity.UnavailableReason, Runtime: session.Runtime}
}
