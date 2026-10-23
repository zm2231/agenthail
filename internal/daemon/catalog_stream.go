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
	followers    map[uint64]chan struct{}
	nextID       uint64
	publishedSeq uint64
}

// catalogFollower follows the catalog journal from its own cursor. Wake
// receives a signal after the hub sees newly committed events; the follower
// then reads them from the journal, so a burst it is slow to read waits in the
// journal instead of ending the follower.
type catalogFollower struct {
	Wake   <-chan struct{}
	Cancel func()
	hub    *catalogHub
	cursor uint64
}

var errCatalogFollowerGap = errors.New("the catalog journal no longer holds the events after this cursor")

// read returns up to limit events after the follower's cursor and advances
// it past them. It returns errCatalogFollowerGap once the journal no longer
// retains the events after the cursor.
func (f *catalogFollower) read(limit int) ([]registry.CatalogEvent, error) {
	window, err := f.hub.registry.CatalogEventsAfter(f.cursor, limit)
	if err != nil {
		return nil, err
	}
	if window.Gap {
		return nil, errCatalogFollowerGap
	}
	if len(window.Events) > 0 {
		f.cursor = window.Events[len(window.Events)-1].Seq
	}
	return window.Events, nil
}

func newCatalogHub(store *registry.Registry) *catalogHub {
	_, latest, err := store.CatalogState()
	if err != nil {
		latest = 0
	}
	return &catalogHub{registry: store, followers: map[uint64]chan struct{}{}, publishedSeq: latest}
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

// flushCommitted wakes the followers once events are committed. Catalog
// writers that do not route through the hub call it after their transaction.
func (h *catalogHub) flushCommitted() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.flushCommittedLocked()
}

func (h *catalogHub) flushCommittedLocked() error {
	_, latest, err := h.registry.CatalogState()
	if err != nil {
		return err
	}
	if latest == h.publishedSeq {
		return nil
	}
	h.publishedSeq = latest
	for _, follower := range h.followers {
		select {
		case follower <- struct{}{}:
		default:
		}
	}
	return nil
}

// subscribe returns the events after after, up to catalogStreamReplayLimit,
// and a follower whose cursor starts after the last of them.
func (h *catalogHub) subscribe(after uint64) (registry.CatalogEventWindow, *catalogFollower, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	window, err := h.registry.CatalogEventsAfter(after, catalogStreamReplayLimit)
	if err != nil {
		return registry.CatalogEventWindow{}, nil, err
	}
	h.nextID++
	id := h.nextID
	wake := make(chan struct{}, 1)
	h.followers[id] = wake
	cursor := after
	if len(window.Events) > 0 {
		cursor = window.Events[len(window.Events)-1].Seq
	}
	follower := &catalogFollower{Wake: wake, hub: h, cursor: cursor}
	follower.Cancel = func() {
		h.mu.Lock()
		if existing, found := h.followers[id]; found {
			delete(h.followers, id)
			close(existing)
		}
		h.mu.Unlock()
	}
	return window, follower, nil
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
	window, follower, err := d.catalog.subscribe(after)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "stream_unavailable", "The catalog journal could not be read.")
		return
	}
	defer follower.Cancel()
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
	write := func(batch []registry.CatalogEvent) bool {
		listed, err := view(batch)
		if err != nil {
			return false
		}
		for _, event := range listed {
			if !d.apiEventStreamAuthorized(r) || writeCatalogStreamEntry(w, event) != nil {
				return false
			}
		}
		return true
	}
	follow := func() bool {
		for {
			batch, err := follower.read(catalogStreamBatch)
			if err != nil {
				d.log.Printf("catalog stream ended: %s", err)
				return false
			}
			if len(batch) > 0 && !write(batch) {
				return false
			}
			if len(batch) < catalogStreamBatch {
				flusher.Flush()
				return true
			}
		}
	}
	if !write(window.Events) || !follow() {
		return
	}
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-follower.Wake:
			if !follow() {
				return
			}
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

func (d *Daemon) discoverCatalog(ctx context.Context) { d.discoverCatalogOf(ctx, nil) }

// discoverCatalogOf runs a discovery pass over the surfaces in only, or over
// every surface when only is nil. Each surface's part of a pass stands alone,
// so a pass of one surface lists, publishes and retires that surface's
// sessions without waiting on the others.
func (d *Daemon) discoverCatalogOf(ctx context.Context, only map[surface.SurfaceKind]bool) {
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
	limits, err := d.limitResumeRows()
	if err != nil {
		d.log.Printf("catalog usage-limit stops: %s", err)
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
		if only != nil && !only[adapter.Name()] {
			continue
		}
		priorStamps := map[string]catalogLiveSession{}
		d.catalogLiveMu.Lock()
		for id, live := range d.catalogLive {
			if live.adapter.Name() == adapter.Name() {
				priorStamps[id] = catalogLiveSession{files: live.files}
			}
		}
		d.catalogLiveMu.Unlock()
		for id, prior := range priorStamps {
			prior.stamps = catalogFileStamps(prior.files)
			priorStamps[id] = prior
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
			var live *catalogLiveSession
			if source != nil {
				live = &catalogLiveSession{adapter: adapter, session: session, identity: identity, alias: alias, open: open, files: source.LocalStatusFiles(session)}
				// Stamps taken before List date the evidence List used, so a
				// file change during the pass still triggers a status refresh.
				if prior, found := priorStamps[session.ID]; found && equalStrings(prior.files, live.files) {
					live.stamps = prior.stamps
				}
			}
			// The entry changes with the row, under the session's waiting
			// lock, so a status publish lands wholly before or after both.
			d.publishCatalogSession(ctx, adapter, session, identity, alias, counts[session.ID], limits[session.ID], open, config, view, observedAt, nil, func(committed bool) {
				d.catalogLiveMu.Lock()
				defer d.catalogLiveMu.Unlock()
				if committed && live != nil {
					d.catalogLive[session.ID] = live
				} else {
					delete(d.catalogLive, session.ID)
				}
			})
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
	if d.catalogProjectUnarchived.CompareAndSwap(true, false) {
		d.publishCatalogPresence()
	}
}

// forgetCatalogLive drops a surface's live sessions that are not in keep, so
// the status pass follows only sessions the last successful pass listed.
func (d *Daemon) forgetCatalogLive(kind surface.SurfaceKind, keep map[string]struct{}) {
	d.catalogLiveMu.Lock()
	forget := map[string]*catalogLiveSession{}
	for id, live := range d.catalogLive {
		if _, found := keep[id]; live.adapter.Name() == kind && !found {
			forget[id] = live
		}
	}
	d.catalogLiveMu.Unlock()
	// Each drop holds the session's waiting lock, which a status publish
	// holds from its liveness check through its write.
	for id, live := range forget {
		unlock := d.lockWaiting(id)
		d.catalogLiveMu.Lock()
		if d.catalogLive[id] == live {
			delete(d.catalogLive, id)
		}
		d.catalogLiveMu.Unlock()
		unlock()
	}
}

// refreshCatalogStatus publishes a status transition as soon as a session's
// local status files show it, without waiting for the next discovery pass.
// Only status, waiting, background and pending-model transitions publish;
// other fields follow discovery.
func (d *Daemon) refreshCatalogStatus(ctx context.Context) {
	defer d.publishUnarchivedProjects()
	published := false
	defer func() {
		if published {
			d.requestObservation()
		}
	}()
	var config *DashboardConfig
	var counts map[string]int
	var limits map[string]*dashboardLimitResume
	var view catalogViewState
	type followed struct {
		id      string
		live    *catalogLiveSession
		session surface.Session
		stamps  []string
	}
	d.catalogLiveMu.Lock()
	sessions := make([]followed, 0, len(d.catalogLive))
	for id, live := range d.catalogLive {
		sessions = append(sessions, followed{id: id, live: live, session: live.session, stamps: live.stamps})
	}
	d.catalogLiveMu.Unlock()
	// current applies change to a followed session unless discovery replaced
	// or dropped it meanwhile; discovery's entry then carries the stamps its
	// listing was read against, so the next pass rereads anything newer.
	current := func(entry followed, change func(*catalogLiveSession)) bool {
		d.catalogLiveMu.Lock()
		defer d.catalogLiveMu.Unlock()
		if d.catalogLive[entry.id] != entry.live {
			return false
		}
		change(entry.live)
		return true
	}
	for _, entry := range sessions {
		live := entry.live
		stamps := catalogFileStamps(live.files)
		// A usage limit lifts at its reset time with no file change, so
		// the row is reread then.
		if entry.stamps != nil && equalStrings(entry.stamps, stamps) && !entry.session.UsageLimit.Lifted(time.Now()) {
			continue
		}
		if !current(entry, func(live *catalogLiveSession) { live.stamps = stamps }) {
			continue
		}
		source, ok := live.adapter.(surface.LocalStatusSource)
		if !ok {
			continue
		}
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		refreshed, err := source.LocalStatus(statusCtx, entry.session)
		cancel()
		if err != nil {
			// The files changed but no longer describe this session, as when
			// its process moved to another conversation; discovery reconciles
			// which processes hold which conversation.
			d.requestCatalogDiscovery()
			continue
		}
		if refreshed.Status == entry.session.Status && surface.SameWaiting(refreshed.WaitingOn, entry.session.WaitingOn) && surface.SameBackground(refreshed.BackgroundTasks, entry.session.BackgroundTasks) && surface.SameUsageLimit(refreshed.UsageLimit, entry.session.UsageLimit) && refreshed.PendingModel == entry.session.PendingModel {
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
			if limits, err = d.limitResumeRows(); err != nil {
				d.log.Printf("catalog usage-limit stops: %s", err)
				return
			}
			if view, err = d.loadCatalogViewState(); err != nil {
				d.log.Printf("catalog view state: %s", err)
				return
			}
		}
		// The check and the write share the session's waiting lock with
		// discovery's change to the entry, so discovery cannot replace or
		// drop the entry between them.
		check := func() bool { return current(entry, func(*catalogLiveSession) {}) }
		d.publishCatalogSession(ctx, live.adapter, refreshed, live.identity, live.alias, counts[entry.id], limits[entry.id], live.open, *config, view, time.Now().UTC(), check, func(committed bool) {
			current(entry, func(live *catalogLiveSession) {
				if committed {
					live.session = refreshed
					published = true
				} else {
					live.stamps = nil
				}
			})
		})
	}
}

// publishCatalogSession records one session row, rebuilding the projection
// when the queue count changed under it. It reports whether the row committed.
// Under the session's waiting lock, check, when set, runs before the row is
// written and stops the write when it fails, and done, when set, runs last
// with whether the row committed.
func (d *Daemon) publishCatalogSession(ctx context.Context, adapter surface.Surface, session surface.Session, identity catalogIdentity, alias string, queueCount int, limitResume *dashboardLimitResume, open bool, config DashboardConfig, view catalogViewState, observedAt time.Time, check func() bool, done func(committed bool)) (committed bool) {
	defer d.lockWaiting(session.ID)()
	if done != nil {
		defer func() { done(committed) }()
	}
	if check != nil && !check() {
		return false
	}
	project, err := json.Marshal(identity.Project)
	if err != nil {
		return false
	}
	checkout, err := json.Marshal(identity.Checkout)
	if err != nil {
		return false
	}
	session.WaitingOn = d.catalogWaiting(ctx, adapter, session)
	if d.catalogSelected != nil {
		d.catalogSelected(session.ID)
	}
	stored := d.storedListing(session.ID)
	for attempt := 0; attempt < 3; attempt++ {
		row := d.catalogSessionProjection(adapter, session, identity, observedAt, alias, queueCount, limitResume, open, config)
		row.HiddenSubagents, row.LiveHiddenSubagents, row.Listed = stored.HiddenSubagents, stored.LiveHiddenSubagents, stored.Listed
		row.Subagents = addSubagentRollups(row.ObservedSubagents, stored.LiveHiddenSubagents)
		if d.settleArchiveActivity([]dashboardSession{row}, view) {
			d.catalogProjectUnarchived.Store(true)
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
		// A stop released with its notice changed the queue this row was
		// read against, so the stop is read again too.
		queueCount, limitResume = conflict.QueueCount, d.limitResumeRow(session.ID)
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
type catalogPresence struct {
	mu        sync.Mutex
	hostEpoch string
	clock     int64
	pass      uint64
	rows      map[string]*presenceRow
	read      []registry.CatalogPresenceRow
	pending   []presencePending
}

// presenceRow is a row's stored projection, decoded, at its generation. A row
// is settled once a pass found its outputs already stored; while its
// generation and outputs stay the same, later passes neither encode nor write
// it.
type presenceRow struct {
	generation int64
	stored     string
	projection dashboardSession
	settled    presenceOutputs
	isSettled  bool
	pass       uint64
}

type presenceOutputs struct {
	queueCount      int
	current         bool
	currentReason   string
	hidden          *surface.SubagentRollup
	liveHidden      *surface.SubagentRollup
	subagents       *surface.SubagentRollup
	listed          bool
	archived        bool
	projectArchived bool
	limitResume     *dashboardLimitResume
}

type presencePending struct {
	row     *presenceRow
	record  int
	adapter surface.Surface
	outputs presenceOutputs
}

func (o presenceOutputs) same(other presenceOutputs) bool {
	return o.queueCount == other.queueCount && o.current == other.current && o.currentReason == other.currentReason &&
		sameSubagentRollup(o.hidden, other.hidden) && sameSubagentRollup(o.liveHidden, other.liveHidden) && sameSubagentRollup(o.subagents, other.subagents) &&
		o.listed == other.listed && o.archived == other.archived && o.projectArchived == other.projectArchived &&
		sameLimitResume(o.limitResume, other.limitResume)
}

func (o presenceOutputs) applyTo(row *dashboardSession) {
	row.QueueCount, row.Current, row.CurrentReason = o.queueCount, o.current, o.currentReason
	row.HiddenSubagents, row.LiveHiddenSubagents, row.Subagents, row.Listed = o.hidden, o.liveHidden, o.subagents, o.listed
	row.LimitResume = o.limitResume
}

func (p *catalogPresence) known(id string, generation int64) bool {
	row, ok := p.rows[id]
	return ok && row.generation == generation
}

func (d *Daemon) publishCatalogPresence() {
	defer d.refreshHostQueueCounts()
	p := &d.presence
	p.mu.Lock()
	defer p.mu.Unlock()
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
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		d.log.Printf("catalog queue counts: %s", err)
		return
	}
	limitResumes, err := d.limitResumeRows()
	if err != nil {
		d.log.Printf("catalog usage-limit stops: %s", err)
		return
	}
	read, err := d.Registry.CatalogPresenceRows(p.read, p.hostEpoch, p.clock, p.known)
	if err != nil {
		d.log.Printf("catalog presence rows: %s", err)
		return
	}
	p.read, p.hostEpoch, p.clock = read.Rows, read.HostEpoch, read.Clock
	if p.rows == nil {
		p.rows = map[string]*presenceRow{}
	}
	p.pass++
	now := time.Now()
	presence := func(adapter surface.Surface, session surface.Session, open bool, count int) (bool, string) {
		return surface.PresenceFor(adapter, session, surface.PresenceInput{QueueCount: count, Open: open, Now: now, CodexRecentHours: config.CodexRecentHours})
	}
	p.pending = p.pending[:0]
	var settling []dashboardSession
	for index := range read.Rows {
		record := &read.Rows[index]
		id := record.Session.ID
		row := p.rows[id]
		if record.FingerprintRead && (row == nil || row.generation != record.Generation || row.stored != record.ProjectionFingerprint) {
			var projection dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &projection) != nil {
				delete(p.rows, id)
				continue
			}
			row = &presenceRow{generation: record.Generation, stored: record.ProjectionFingerprint, projection: projection}
			p.rows[id] = row
		}
		if row == nil {
			continue
		}
		row.pass = p.pass
		adapter, err := d.adapters().For(&record.Session)
		if err != nil {
			continue
		}
		outputs := presenceOutputs{queueCount: counts[id], listed: read.Listing.Current(id), hidden: read.Listing.HiddenSubagents(id), liveHidden: read.Listing.LiveHiddenSubagents(id), limitResume: limitResumes[id]}
		outputs.current, outputs.currentReason = presence(adapter, record.Session, row.projection.Open, outputs.queueCount)
		outputs.subagents = addSubagentRollups(row.projection.ObservedSubagents, outputs.liveHidden)
		if archived, _ := catalogViewArchive(&row.projection, view); archived {
			candidate := row.projection
			outputs.applyTo(&candidate)
			settling = append(settling, candidate)
		}
		p.pending = append(p.pending, presencePending{row: row, record: index, adapter: adapter, outputs: outputs})
	}
	for id, row := range p.rows {
		if row.pass != p.pass {
			delete(p.rows, id)
		}
	}
	d.settleArchiveActivity(settling, view)
	for _, pending := range p.pending {
		row, outputs := pending.row, pending.outputs
		outputs.archived, outputs.projectArchived = catalogViewArchive(&row.projection, view)
		if row.isSettled && row.settled.same(outputs) {
			continue
		}
		session := read.Rows[pending.record].Session
		for attempt := 0; attempt < 3; attempt++ {
			projection := row.projection
			outputs.applyTo(&projection)
			applyCatalogView(&projection, view)
			fingerprint, err := json.Marshal(projection)
			if err != nil {
				break
			}
			if string(fingerprint) == row.stored {
				row.settled, row.isSettled = outputs, true
				break
			}
			if err := d.catalog.updateProjection(session.ID, row.stored, string(fingerprint)); err == nil {
				break
			} else {
				var conflict *registry.CatalogQueueProjectionConflict
				if !errors.As(err, &conflict) {
					d.log.Printf("catalog view projection %s: %s", d.resolveDisplay(session.ID), err)
					break
				}
				outputs.queueCount, outputs.limitResume = conflict.QueueCount, d.limitResumeRow(session.ID)
				outputs.current, outputs.currentReason = presence(pending.adapter, session, row.projection.Open, conflict.QueueCount)
				if attempt == 2 {
					d.log.Printf("catalog queue projection %s remained unstable", d.resolveDisplay(session.ID))
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

func (d *Daemon) catalogSessionProjection(adapter surface.Surface, session surface.Session, identity catalogIdentity, observedAt time.Time, alias string, queueCount int, limitResume *dashboardLimitResume, open bool, config DashboardConfig) dashboardSession {
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
	return dashboardSession{ID: session.ID, Surface: session.Surface, RuntimeKind: session.RuntimeKind, ConfiguredModel: session.ConfiguredModel, PendingModel: session.PendingModel, ClaudeConfigRoot: configRoot, Name: session.Name, Cwd: session.Cwd, Alias: alias, Status: session.Status, WaitingOn: session.WaitingOn, BackgroundTasks: session.BackgroundTasks, UsageLimit: session.UsageLimit, LimitResume: limitResume, LastActive: session.LastActive, QueueCount: queueCount, Open: open, Current: current, CurrentReason: reason, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: session.Source, Transport: session.Transport, ForkedFrom: session.ForkedFrom, Project: identity.Project, Checkout: &identity.Checkout, ObservedAt: observedAt, UnavailableReason: identity.UnavailableReason, Runtime: session.Runtime, Subagent: session.Subagent, Delegated: session.Delegated(), Subagents: session.Subagents, Holders: session.Holders, ObservedSubagents: session.Subagents, Host: &host}
}

type catalogStoredListing struct {
	HiddenSubagents     *surface.SubagentRollup `json:"hiddenSubagents"`
	LiveHiddenSubagents *surface.SubagentRollup `json:"liveHiddenSubagents"`
	Listed              bool                    `json:"listed"`
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

func addSubagentRollups(observed, liveHidden *surface.SubagentRollup) *surface.SubagentRollup {
	if liveHidden == nil {
		return observed
	}
	sum := *liveHidden
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
