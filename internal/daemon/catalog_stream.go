package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

func (h *catalogHub) recordDiscovery(kind surface.SurfaceKind, seen map[string]struct{}, observedAt time.Time, complete bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.registry.RecordCatalogDiscovery(kind, seen, observedAt, complete, 2)
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
	writeSSEOpen(w)
	mirror := catalogMirror(r.Context())
	view := func(batch []registry.CatalogEvent) ([]registry.CatalogEvent, error) {
		if mirror {
			return batch, nil
		}
		return d.listedCatalogEvents(batch)
	}
	backlog, err := view(window.Events)
	if err != nil {
		return
	}
	for _, event := range backlog {
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
			if !open {
				return
			}
			batch := []registry.CatalogEvent{event}
		drain:
			for len(batch) < catalogStreamBatch {
				select {
				case next, more := <-events:
					if !more {
						break drain
					}
					batch = append(batch, next)
				default:
					break drain
				}
			}
			listed, err := view(batch)
			if err != nil {
				return
			}
			for _, event := range listed {
				if !d.apiEventStreamAuthorized(r) || writeCatalogStreamEntry(w, event) != nil {
					return
				}
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

const catalogStreamBatch = 256

// listedCatalogEvents is the catalog stream a device sees: the session list,
// not every catalog row. An upsert of a session outside the current list, a
// hidden subagent or a retired row, reaches it as a removal under the same
// sequence. The presence pass republishes a row when it
// enters or leaves the list and an ancestor when its hidden count changes, so
// each of those changes arrives as its own event. Without the listing the
// stream ends rather than show a device rows it must not list.
func (d *Daemon) listedCatalogEvents(batch []registry.CatalogEvent) ([]registry.CatalogEvent, error) {
	listing, err := d.Registry.CatalogListing()
	if err != nil {
		d.log.Printf("catalog listing: %s", err)
		return nil, err
	}
	listed := make([]registry.CatalogEvent, 0, len(batch))
	for _, event := range batch {
		if event.Type == "session.upserted" && !listing.CurrentEntity(event.EntityID) {
			payload, err := json.Marshal(map[string]string{"sessionId": event.EntityID, "reason": "unlisted"})
			if err != nil {
				continue
			}
			event.Type, event.Payload = "session.removed", payload
		}
		listed = append(listed, event)
	}
	return listed, nil
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

// catalogLiveSession is the last published discovery input for a session.
// The status pass rebuilds the row from it when local status evidence changes.
type catalogLiveSession struct {
	adapter  surface.Surface
	session  surface.Session
	identity catalogIdentity
	alias    string
	open     bool
	files    []string
	stamps   []string
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
	view, err := d.loadCatalogViewState()
	if err != nil {
		d.log.Printf("catalog view state: %s", err)
		return
	}
	if projects, err := d.Registry.ListProjects(); err != nil {
		d.log.Printf("catalog projects: %s", err)
	} else {
		d.refreshProjectRepositories(ctx, projects, time.Now())
		d.catalogIdentities.setProjects(projects)
	}
	for _, adapter := range d.Surfaces {
		priorStamps := map[string]catalogLiveSession{}
		for id, live := range d.catalogLive {
			if live.adapter.Name() == adapter.Name() {
				priorStamps[id] = catalogLiveSession{files: live.files, stamps: catalogFileStamps(live.files)}
			}
		}
		operationCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		complete := true
		var sessions []surface.Session
		var err error
		if lister, ok := adapter.(surface.CatalogLister); ok {
			sessions, complete, err = lister.CatalogList(operationCtx)
		} else {
			sessions, err = adapter.List(operationCtx)
		}
		cancel()
		health := d.recordSurfaceHealth(ctx, adapter, err)
		if err != nil {
			d.forgetCatalogLive(adapter.Name(), nil)
			observedAt := time.Now().UTC()
			if markErr := d.catalog.markDiscoveryFailure(adapter.Name(), "catalog discovery failed", observedAt); markErr != nil {
				d.log.Printf("catalog discovery failure state: %s", markErr)
			}
			d.publishSurfaceHealth(health, observedAt)
			continue
		}
		observedAt := time.Now().UTC()
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
		openSessions := surface.OpenSessions(ctx, adapter, sessions)
		source, _ := adapter.(surface.LocalStatusSource)
		for _, session := range sessions {
			if session.Runtime == nil {
				session.Runtime = &surface.Runtime{Launcher: surface.LauncherExternal, Focusable: false}
			}
			seen[session.ID] = struct{}{}
			identityCtx, identityCancel := context.WithTimeout(ctx, 3*time.Second)
			identity := d.catalogIdentities.identity(identityCtx, session, observedAt, func() *CatalogProject { return d.storedCatalogProject(session.ID) })
			identityCancel()
			open := openSessions[session.ID]
			alias := aliasByID[session.ID]
			if !d.publishCatalogSession(ctx, adapter, session, identity, alias, counts[session.ID], open, config, view, observedAt) {
				delete(d.catalogLive, session.ID)
				continue
			}
			if source == nil {
				delete(d.catalogLive, session.ID)
				continue
			}
			live := &catalogLiveSession{adapter: adapter, session: session, identity: identity, alias: alias, open: open, files: source.LocalStatusFiles(session)}
			// Stamps taken before List date the evidence List used, so a file
			// change during the pass still triggers a status refresh.
			if prior, found := priorStamps[session.ID]; found && equalStrings(prior.files, live.files) {
				live.stamps = prior.stamps
			}
			d.catalogLive[session.ID] = live
		}
		d.forgetCatalogLive(adapter.Name(), seen)
		if err := d.catalog.recordDiscovery(adapter.Name(), seen, observedAt, complete); err != nil {
			d.log.Printf("catalog discovery %s: %s", adapter.Name(), err)
		}
		d.publishSurfaceHealth(health, time.Now().UTC())
	}
	d.catalogIdentities.prune(time.Now())
	d.publishUnarchivedProjects()
}

// publishUnarchivedProjects republishes the other rows of a project that a
// session's activity unarchived during a catalog pass.
func (d *Daemon) publishUnarchivedProjects() {
	if d.catalogProjectUnarchived {
		d.catalogProjectUnarchived = false
		d.publishCatalogPresence()
	}
}

// forgetCatalogLive drops a surface's live sessions that are not in keep, so
// the status pass follows only sessions the last successful pass listed.
func (d *Daemon) forgetCatalogLive(kind surface.SurfaceKind, keep map[string]struct{}) {
	for id, live := range d.catalogLive {
		if live.adapter.Name() != kind {
			continue
		}
		if _, found := keep[id]; !found {
			delete(d.catalogLive, id)
		}
	}
}

// refreshCatalogStatus publishes a status transition as soon as a session's
// local status files show it, without waiting for the next discovery pass.
// Only status, waiting, background and pending-model transitions publish;
// other fields follow discovery.
func (d *Daemon) refreshCatalogStatus(ctx context.Context) {
	defer d.publishUnarchivedProjects()
	var config *DashboardConfig
	var counts map[string]int
	var view catalogViewState
	for id, live := range d.catalogLive {
		stamps := catalogFileStamps(live.files)
		if live.stamps != nil && equalStrings(live.stamps, stamps) {
			continue
		}
		live.stamps = stamps
		source, ok := live.adapter.(surface.LocalStatusSource)
		if !ok {
			continue
		}
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		refreshed, err := source.LocalStatus(statusCtx, live.session)
		cancel()
		if err != nil {
			// The files changed but no longer describe this session, as when
			// its process moved to another conversation; discovery reconciles
			// which processes hold which conversation.
			d.requestCatalogDiscovery()
			continue
		}
		if refreshed.Status == live.session.Status && surface.SameWaiting(refreshed.WaitingOn, live.session.WaitingOn) && surface.SameBackground(refreshed.BackgroundTasks, live.session.BackgroundTasks) && refreshed.PendingModel == live.session.PendingModel {
			continue
		}
		if config == nil {
			loaded, err := LoadDashboardConfig()
			if err != nil {
				d.log.Printf("catalog config: %s", err)
				return
			}
			config = &loaded
			if counts, err = d.Registry.QueueCounts(); err != nil {
				d.log.Printf("catalog queue counts: %s", err)
				return
			}
			if view, err = d.loadCatalogViewState(); err != nil {
				d.log.Printf("catalog view state: %s", err)
				return
			}
		}
		if d.publishCatalogSession(ctx, live.adapter, refreshed, live.identity, live.alias, counts[id], live.open, *config, view, time.Now().UTC()) {
			live.session = refreshed
		} else {
			live.stamps = nil
		}
	}
}

// publishCatalogSession records one session row, rebuilding the projection
// when the queue count changed under it. It reports whether the row committed.
func (d *Daemon) publishCatalogSession(ctx context.Context, adapter surface.Surface, session surface.Session, identity catalogIdentity, alias string, queueCount int, open bool, config DashboardConfig, view catalogViewState, observedAt time.Time) bool {
	project, err := json.Marshal(identity.Project)
	if err != nil {
		return false
	}
	checkout, err := json.Marshal(identity.Checkout)
	if err != nil {
		return false
	}
	defer d.lockWaiting(session.ID)()
	session.WaitingOn = d.catalogWaiting(ctx, adapter, session)
	if d.catalogSelected != nil {
		d.catalogSelected(session.ID)
	}
	stored := d.storedListing(session.ID)
	for attempt := 0; attempt < 3; attempt++ {
		row := d.catalogSessionProjection(adapter, session, identity, observedAt, alias, queueCount, open, config)
		row.HiddenSubagents, row.Listed = stored.HiddenSubagents, stored.Listed
		row.Subagents = addSubagentRollups(row.ObservedSubagents, stored.HiddenSubagents)
		if d.settleArchiveActivity([]dashboardSession{row}, view) {
			d.catalogProjectUnarchived = true
		}
		applyCatalogView(&row, view)
		payload, err := json.Marshal(map[string]any{"session": row})
		if err != nil {
			return false
		}
		projection := row
		projection.ObservedAt = time.Time{}
		fingerprint, err := json.Marshal(projection)
		if err != nil {
			return false
		}
		key := "session.upserted:" + session.ID
		_, _, publishErr := d.catalog.publishSession(registry.CatalogSessionState{Session: session, Project: project, Checkout: checkout, UnavailableReason: identity.UnavailableReason, ObservedAt: observedAt, ProjectionFingerprint: string(fingerprint)}, registry.CatalogEvent{DedupeKey: key, Type: "session.upserted", EntityID: session.ID, Payload: payload})
		if publishErr == nil {
			return true
		}
		var conflict *registry.CatalogQueueProjectionConflict
		if !errors.As(publishErr, &conflict) {
			d.log.Printf("catalog session %s: %s", d.resolveDisplay(session.ID), publishErr)
			return false
		}
		queueCount = conflict.QueueCount
	}
	d.log.Printf("catalog session %s remained unstable while publishing queue projection", d.resolveDisplay(session.ID))
	return false
}

// catalogFileStamps fingerprints each file by size and modification time; a
// missing file has an empty stamp.
func catalogFileStamps(paths []string) []string {
	stamps := make([]string, len(paths))
	for index, path := range paths {
		if info, err := os.Stat(path); err == nil {
			stamps[index] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		}
	}
	return stamps
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (d *Daemon) publishSurfaceHealth(health dashboardSurface, observedAt time.Time) {
	payload, _ := json.Marshal(map[string]any{"surface": health.Name, "health": health.Health, "detail": health.HealthDetail, "runtime": health.Runtime, "observedAt": observedAt.Format(time.RFC3339Nano)})
	_, _, _ = d.catalog.publishSurface(registry.CatalogSurfaceState{Surface: surface.SurfaceKind(health.Name), Health: health.Health, Detail: health.HealthDetail, ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: "surface.health:" + health.Name + ":" + health.Health, Type: "surface.health", EntityID: health.Name, Payload: payload})
}

// publishCatalogPresence keeps each local row's queue count, presence, place
// in the session list, hidden subagent count and archive state current between
// discovery passes, so a new queue count, a changed Codex window, a lapsed
// recent session, a subagent entering or leaving the list, or an archive change
// is published on the next tick.
func (d *Daemon) publishCatalogPresence() {
	defer d.refreshHostQueueCounts()
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
	view, err := d.loadCatalogViewState()
	if err != nil {
		d.log.Printf("catalog view state: %s", err)
		return
	}
	now := time.Now()
	type localRow struct {
		record     registry.CatalogSessionState
		projection dashboardSession
		adapter    surface.Surface
	}
	present := func(row *localRow, count int) {
		row.projection.QueueCount = count
		row.projection.Current, row.projection.CurrentReason = surface.PresenceFor(row.adapter, row.record.Session, surface.PresenceInput{QueueCount: count, Open: row.projection.Open, Now: now, CodexRecentHours: config.CodexRecentHours})
	}
	rows := make([]localRow, 0, len(snapshot.Sessions))
	projections := make([]dashboardSession, 0, len(snapshot.Sessions))
	for _, record := range snapshot.Sessions {
		var projection dashboardSession
		if record.HostID != "" || json.Unmarshal([]byte(record.ProjectionFingerprint), &projection) != nil {
			continue
		}
		adapter, err := d.adapters().For(&record.Session)
		if err != nil {
			continue
		}
		row := localRow{record: record, projection: projection, adapter: adapter}
		present(&row, counts[record.Session.ID])
		hidden := snapshot.Listing.HiddenSubagents(record.Session.ID)
		row.projection.HiddenSubagents = hidden
		row.projection.Subagents = addSubagentRollups(projection.ObservedSubagents, hidden)
		row.projection.Listed = snapshot.Listing.Current(record.Session.ID)
		rows = append(rows, row)
		projections = append(projections, row.projection)
	}
	d.settleArchiveActivity(projections, view)
	for _, row := range rows {
		for attempt := 0; attempt < 3; attempt++ {
			applyCatalogView(&row.projection, view)
			fingerprint, err := json.Marshal(row.projection)
			if err != nil || string(fingerprint) == row.record.ProjectionFingerprint {
				break
			}
			if err := d.catalog.updateProjection(row.record.Session.ID, row.record.ProjectionFingerprint, string(fingerprint)); err == nil {
				break
			} else {
				var conflict *registry.CatalogQueueProjectionConflict
				if !errors.As(err, &conflict) {
					d.log.Printf("catalog view projection %s: %s", d.resolveDisplay(row.record.Session.ID), err)
					break
				}
				present(&row, conflict.QueueCount)
				if attempt == 2 {
					d.log.Printf("catalog queue projection %s remained unstable", d.resolveDisplay(row.record.Session.ID))
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
	now := time.Now()
	for _, item := range pending {
		if now.After(item.ExpiresAt.Add(pendingLaunchRetention)) {
			if err := d.Registry.DropPendingLaunch(item); err != nil {
				d.log.Printf("drop pending launch %d: %s", item.ID, err)
			} else {
				d.log.Printf("pending %s launch %d was not found within %s; dropped", item.Launcher, item.ID, now.Sub(item.LaunchedAt).Round(time.Second))
			}
			continue
		}
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
				named, err := d.Registry.RegisterSessionAndDeletePending(candidate, item)
				if err != nil {
					d.log.Printf("correlate pending launch %d: %s", item.ID, err)
					continue
				}
				d.log.Printf("pending %s launch %d found after %s", item.Launcher, item.ID, time.Since(item.LaunchedAt).Round(time.Second))
				if item.Alias.Name != "" && !named {
					d.log.Printf("pending %s launch %d: @%s was taken after its reservation lapsed; the session has no name", item.Launcher, item.ID, item.Alias.Name)
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

func (d *Daemon) storedCatalogProject(sessionID string) *CatalogProject {
	raw, err := d.Registry.CatalogSessionProject(sessionID)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var project *CatalogProject
	if json.Unmarshal(raw, &project) != nil || project == nil || project.ID == "" || project.LocalID == "" {
		return nil
	}
	return project
}

func (d *Daemon) catalogSessionProjection(adapter surface.Surface, session surface.Session, identity catalogIdentity, observedAt time.Time, alias string, queueCount int, open bool, config DashboardConfig) dashboardSession {
	if session.Runtime == nil {
		session.Runtime = &surface.Runtime{Launcher: surface.LauncherExternal, Focusable: false}
	}
	current, reason := surface.PresenceFor(adapter, session, surface.PresenceInput{QueueCount: queueCount, Open: open, Now: observedAt, CodexRecentHours: config.CodexRecentHours})
	host := d.localHost()
	effective := surface.EffectiveCapabilities(adapter, &session)
	var configRoot *surface.SessionConfigRoot
	if session.Surface == surface.KindClaude {
		configRoot = surface.ClaudeSessionConfigRoot(d.claudeConfigRoots(), session.Transcript)
	}
	return dashboardSession{ID: session.ID, Surface: session.Surface, RuntimeKind: session.RuntimeKind, ConfiguredModel: session.ConfiguredModel, PendingModel: session.PendingModel, ClaudeConfigRoot: configRoot, Name: session.Name, Cwd: session.Cwd, Alias: alias, Status: session.Status, WaitingOn: session.WaitingOn, BackgroundTasks: session.BackgroundTasks, LastActive: session.LastActive, QueueCount: queueCount, Open: open, Current: current, CurrentReason: reason, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: session.Source, Transport: session.Transport, ForkedFrom: session.ForkedFrom, Project: identity.Project, Checkout: &identity.Checkout, ObservedAt: observedAt, UnavailableReason: identity.UnavailableReason, Runtime: session.Runtime, Subagent: session.Subagent, Delegated: session.Delegated(), Subagents: session.Subagents, Holders: session.Holders, ObservedSubagents: session.Subagents, Host: &host}
}

type catalogStoredListing struct {
	HiddenSubagents *surface.SubagentRollup `json:"hiddenSubagents"`
	Listed          bool                    `json:"listed"`
}

// storedListing carries a row's place in the session list and its hidden
// subagent count forward when discovery republishes it; the presence pass
// recomputes both from the listing.
func (d *Daemon) storedListing(id string) catalogStoredListing {
	var stored catalogStoredListing
	fingerprint, found, err := d.Registry.CatalogProjectionFingerprint(id)
	if err != nil || !found {
		return stored
	}
	_ = json.Unmarshal([]byte(fingerprint), &stored)
	return stored
}

func addSubagentRollups(observed, hidden *surface.SubagentRollup) *surface.SubagentRollup {
	if hidden == nil {
		return observed
	}
	sum := *hidden
	if observed != nil {
		sum.Count += observed.Count
		sum.Working += observed.Working
	}
	return &sum
}

func sameSubagentRollup(a, b *surface.SubagentRollup) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
