package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
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

func (h *catalogHub) updateProjection(sessionID, priorFingerprint, nextFingerprint string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, _, err := h.registry.UpdateCatalogSessionProjection(sessionID, priorFingerprint, nextFingerprint); err != nil {
		return err
	}
	return h.flushCommittedLocked()
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

func (h *catalogHub) markDiscoveryFailure(kind surface.SurfaceKind, reason string, observedAt time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := h.registry.MarkCatalogDiscoveryFailure(kind, reason, observedAt); err != nil {
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
		d.recordSurfaceHealth(ctx, adapter, err)
		if err != nil {
			observedAt := time.Now().UTC()
			if markErr := d.catalog.markDiscoveryFailure(adapter.Name(), "catalog discovery failed", observedAt); markErr != nil {
				d.log.Printf("catalog discovery failure state: %s", markErr)
			}
			payload, _ := json.Marshal(map[string]string{"surface": string(adapter.Name()), "health": "unavailable", "detail": "catalog discovery failed", "observedAt": observedAt.Format(time.RFC3339Nano)})
			_, _, _ = d.catalog.publishSurface(registry.CatalogSurfaceState{Surface: adapter.Name(), Health: "unavailable", Detail: "catalog discovery failed", ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: "surface.health:" + string(adapter.Name()) + ":unavailable", Type: "surface.health", EntityID: string(adapter.Name()), Payload: payload})
			continue
		}
		ids := make([]string, 0, len(sessions))
		for _, session := range sessions {
			ids = append(ids, session.ID)
		}
		runtimes, runtimeErr := d.Registry.SessionRuntimes(ids)
		if runtimeErr != nil {
			d.log.Printf("catalog session runtimes: %s", runtimeErr)
			continue
		}
		for index := range sessions {
			if runtime, found := runtimes[sessions[index].ID]; found {
				sessions[index].Runtime = runtime
			}
		}
		seen := make(map[string]struct{}, len(sessions))
		locateCtx, locateCancel := context.WithTimeout(ctx, 12*time.Second)
		d.correlatePendingLaunches(locateCtx, sessions)
		d.correlateObservedTerminalLaunches(locateCtx, sessions)
		locateCancel()
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
			queueCount := counts[session.ID]
			for attempt := 0; attempt < 3; attempt++ {
				row := d.catalogSessionProjection(adapter, session, identity, observedAt, aliasByID[session.ID], queueCount, open, config)
				payload, err := json.Marshal(map[string]any{"session": row})
				if err != nil {
					break
				}
				projection := row
				projection.ObservedAt = time.Time{}
				fingerprint, err := json.Marshal(projection)
				if err != nil {
					break
				}
				key := "session.upserted:" + session.ID
				_, _, publishErr := d.catalog.publishSession(registry.CatalogSessionState{Session: session, HostProject: hostProject, Checkout: checkout, UnavailableReason: identity.UnavailableReason, ObservedAt: observedAt, ProjectionFingerprint: string(fingerprint)}, registry.CatalogEvent{DedupeKey: key, Type: "session.upserted", EntityID: session.ID, Payload: payload})
				if publishErr == nil {
					break
				}
				var conflict *registry.CatalogQueueProjectionConflict
				if !errors.As(publishErr, &conflict) {
					d.log.Printf("catalog session %s: %s", d.resolveDisplay(session.ID), publishErr)
					break
				}
				queueCount = conflict.QueueCount
				if attempt == 2 {
					d.log.Printf("catalog session %s remained unstable while publishing queue projection", d.resolveDisplay(session.ID))
				}
			}
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

func (d *Daemon) publishCatalogQueueCounts() {
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		d.log.Printf("catalog queue counts: %s", err)
		return
	}
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		d.log.Printf("catalog queue snapshot: %s", err)
		return
	}
	config, err := LoadDashboardConfig()
	if err != nil {
		d.log.Printf("catalog config: %s", err)
		return
	}
	now := time.Now()
	for _, record := range snapshot.Sessions {
		count := counts[record.Session.ID]
		for attempt := 0; attempt < 3; attempt++ {
			var projection dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &projection) != nil {
				break
			}
			if projection.QueueCount == count {
				break
			}
			projection.QueueCount = count
			projection.Current, projection.CurrentReason = dashboardSessionPresence(record.Session, count, projection.Open, config.CodexRecentHours, now)
			fingerprint, err := json.Marshal(projection)
			if err != nil {
				break
			}
			if err := d.catalog.updateProjection(record.Session.ID, record.ProjectionFingerprint, string(fingerprint)); err == nil {
				break
			} else {
				var conflict *registry.CatalogQueueProjectionConflict
				if !errors.As(err, &conflict) {
					d.log.Printf("catalog queue projection %s: %s", d.resolveDisplay(record.Session.ID), err)
					break
				}
				count = conflict.QueueCount
				if attempt == 2 {
					d.log.Printf("catalog queue projection %s remained unstable", d.resolveDisplay(record.Session.ID))
				}
			}
		}
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
		candidates := make([]surface.Session, 0, len(sessions))
		for _, session := range sessions {
			if session.Surface != item.Agent || (item.Cwd != "" && filepath.Clean(session.Cwd) != filepath.Clean(item.Cwd)) {
				continue
			}
			candidates = append(candidates, session)
		}
		locations := launcher.Locate(ctx, candidates)
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
			if sessions[index].ID == matched && sessions[index].Surface == item.Agent && filepath.Clean(sessions[index].Cwd) == filepath.Clean(item.Cwd) {
				located := locations[matched]
				locationsCopy := located
				candidate := sessions[index]
				candidate.Runtime = &surface.Runtime{Launcher: item.Launcher, Location: &locationsCopy, Focusable: true}
				if err := d.Registry.RegisterSessionAndDeletePending(candidate, item.ID, item.Alias); err != nil {
					var aliasErr registry.AliasTakenError
					if errors.As(err, &aliasErr) {
						d.errorMu.Lock()
						first := !d.pendingAliasWarns[item.ID]
						d.pendingAliasWarns[item.ID] = true
						d.errorMu.Unlock()
						if first {
							d.log.Printf("pending launch %d awaiting alias %q: %s", item.ID, item.Alias, err)
						}
					} else {
						d.log.Printf("correlate pending launch %d: %s", item.ID, err)
					}
					continue
				}
				sessions[index].Runtime = candidate.Runtime
				break
			}
		}
	}
}

func (d *Daemon) correlateObservedTerminalLaunches(ctx context.Context, sessions []surface.Session) {
	if d.transportResolver == nil {
		return
	}
	type observedLocation struct {
		launcher string
		location surface.Location
	}
	observed := make(map[string][]observedLocation)
	for _, launcherID := range []string{surface.LauncherCMUX, surface.LauncherTMUX} {
		launcher, ok := d.transportResolver.Launcher(launcherID)
		if !ok {
			continue
		}
		for sessionID, location := range launcher.Locate(ctx, sessions) {
			observed[sessionID] = append(observed[sessionID], observedLocation{launcher: launcherID, location: location})
		}
	}
	for index := range sessions {
		if sessions[index].Runtime != nil && sessions[index].Runtime.Launcher != surface.LauncherExternal {
			continue
		}
		matches := observed[sessions[index].ID]
		if len(matches) != 1 {
			continue
		}
		location := matches[0].location
		candidate := sessions[index]
		candidate.Runtime = &surface.Runtime{Launcher: matches[0].launcher, Location: &location, Focusable: true}
		if err := d.Registry.RegisterSession(candidate); err != nil {
			d.log.Printf("persist discovered terminal runtime for %s: %s", candidate.ID, err)
			continue
		}
		sessions[index].Runtime = candidate.Runtime
	}
}

func (d *Daemon) catalogSessionProjection(adapter surface.Surface, session surface.Session, identity catalogIdentity, observedAt time.Time, alias string, queueCount int, open bool, config DashboardConfig) dashboardSession {
	if session.Runtime == nil {
		session.Runtime = &surface.Runtime{Launcher: surface.LauncherExternal, Focusable: false}
	}
	current, reason := dashboardSessionPresence(session, queueCount, open, config.CodexRecentHours, observedAt)
	effective := surface.EffectiveCapabilities(&session, adapter.Capabilities())
	return dashboardSession{ID: session.ID, Surface: session.Surface, Name: session.Name, Cwd: session.Cwd, Alias: alias, Status: session.Status, LastActive: session.LastActive, QueueCount: queueCount, Open: open, Current: current, CurrentReason: reason, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: session.Source, Transport: session.Transport, HostProject: &identity.HostProject, Checkout: &identity.Checkout, ObservedAt: observedAt, UnavailableReason: identity.UnavailableReason, Runtime: session.Runtime}
}
