package daemon

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	catalogOmissionThreshold = 2
	catalogEndedRecheck      = time.Hour
	catalogRestoreBatch      = 25
	catalogRestoreBudget     = 5 * time.Second
)

type catalogRestore struct {
	id      string
	changed time.Time
}

type catalogEnded struct {
	mu      sync.Mutex
	due     map[surface.SurfaceKind]time.Time
	retry   map[surface.SurfaceKind]time.Duration
	backlog map[surface.SurfaceKind][]catalogRestore
	hidden  map[surface.SurfaceKind][]string
}

func newCatalogEnded() *catalogEnded {
	return &catalogEnded{due: map[surface.SurfaceKind]time.Time{}, retry: map[surface.SurfaceKind]time.Duration{}, backlog: map[surface.SurfaceKind][]catalogRestore{}, hidden: map[surface.SurfaceKind][]string{}}
}

func (e *catalogEnded) setHidden(kind surface.SurfaceKind, ids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hidden[kind] = ids
}

func (e *catalogEnded) takeHidden(kind surface.SurfaceKind, limit int) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := e.hidden[kind]
	if len(ids) == 0 {
		return nil
	}
	limit = min(limit, len(ids))
	e.hidden[kind] = ids[limit:]
	return ids[:limit]
}

func (e *catalogEnded) recheckDue(kind surface.SurfaceKind, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	due, found := e.due[kind]
	return !found || !now.Before(due)
}

func (e *catalogEnded) markRechecked(kind surface.SurfaceKind, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.due[kind] = at.Add(catalogEndedRecheck)
	delete(e.retry, kind)
}

// A failed recheck runs again on the next pass, then after a wait that
// doubles from one pass interval up to the hour, so one that fails every time
// does not stat every kept transcript on every pass.
func (e *catalogEnded) recheckFailed(kind surface.SurfaceKind, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	wait := e.retry[kind]
	e.due[kind] = at.Add(wait)
	e.retry[kind] = min(max(2*wait, catalogDiscoveryInterval), catalogEndedRecheck)
}

func (e *catalogEnded) setBacklog(kind surface.SurfaceKind, items []catalogRestore) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.backlog[kind] = items
}

func (e *catalogEnded) takeBacklog(kind surface.SurfaceKind, limit int) []catalogRestore {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := e.backlog[kind]
	if len(items) == 0 {
		return nil
	}
	limit = min(limit, len(items))
	e.backlog[kind] = items[limit:]
	return items[:limit]
}

// Kept rows are statted only on a recheck, so a pass costs the same however many there are.
func (d *Daemon) recordEndedDiscovery(kind surface.SurfaceKind, keeper surface.EndedConversations, seen, hidden map[string]struct{}, observedAt time.Time) (err error) {
	remove := d.catalogEnded.takeHidden(kind, catalogHideBatch)
	for id := range hidden {
		remove = append(remove, id)
	}
	if err := d.catalog.removeHidden(remove); err != nil {
		return err
	}
	recheck := d.catalogEnded.recheckDue(kind, observedAt)
	if recheck {
		defer func() {
			if err != nil {
				d.catalogEnded.recheckFailed(kind, time.Now())
			} else {
				d.catalogEnded.markRechecked(kind, time.Now())
			}
		}()
	}
	candidates, err := d.Registry.CatalogOmissionCandidates(string(kind), catalogOmissionThreshold, recheck)
	if err != nil {
		return err
	}
	gone := map[string]struct{}{}
	for _, row := range candidates {
		if _, live := seen[row.ID]; live {
			continue
		}
		if resumable, _ := keeper.EndedConversation(row.Transcript); !resumable {
			gone[row.ID] = struct{}{}
		}
	}
	if err := d.catalog.recordEndedDiscovery(kind, seen, observedAt, gone); err != nil {
		return err
	}
	if !recheck {
		return nil
	}
	if err := d.recordUnseenConversations(kind, keeper); err != nil {
		return err
	}
	if reader, reads := keeper.(surface.UnseenConversations); reads {
		if err := d.recordConversationEntrypoints(kind, reader, time.Now().Add(catalogRestoreBudget)); err != nil {
			return err
		}
	}
	if err := d.queueHiddenRemovals(kind); err != nil {
		return err
	}
	missing, err := d.Registry.EndedSessionsWithoutCatalogRow(string(kind))
	if err != nil {
		return err
	}
	backlog := []catalogRestore{}
	for _, row := range missing {
		resumable, changed := keeper.EndedConversation(row.Transcript)
		if resumable && !changed.IsZero() {
			backlog = append(backlog, catalogRestore{id: row.ID, changed: changed})
		}
	}
	d.catalogEnded.setBacklog(kind, backlog)
	return nil
}

// Live sessions were registered earlier in the pass, so known covers them.
func (d *Daemon) recordUnseenConversations(kind surface.SurfaceKind, keeper surface.EndedConversations) error {
	lister, lists := keeper.(surface.UnseenConversations)
	if !lists {
		return nil
	}
	known, err := d.Registry.LocalSessionIDs(string(kind))
	if err != nil {
		return err
	}
	unseen := lister.UnseenConversations(func(id string) bool { return known[id] })
	if len(unseen) == 0 {
		return nil
	}
	added, err := d.Registry.RecordUnseenSessions(unseen)
	if err == nil && added > 0 {
		d.log.Printf("catalog: recorded %d %s conversations from their transcripts", added, kind)
	}
	return err
}

// A restore that lost to another write goes back on the backlog, so the next
// pass restores the session from what that write left.
func (d *Daemon) restoreEndedSessions(kind surface.SurfaceKind, keeper surface.EndedConversations, seen map[string]struct{}, publish func(ended, read surface.Session) bool) {
	leads, err := d.Registry.LeadSessionsWithoutCatalogRow(string(kind))
	if err != nil {
		d.log.Printf("catalog leads without a row: %s", err)
		return
	}
	for _, row := range leads {
		resumable, changed := keeper.EndedConversation(row.Transcript)
		if !resumable {
			continue
		}
		if _, live := seen[row.ID]; !live {
			d.restoreEndedSession(catalogRestore{id: row.ID, changed: changed}, publish)
		}
	}
	deadline := time.Now().Add(catalogRestoreBudget)
	batch := d.catalogEnded.takeBacklog(kind, catalogRestoreBatch)
	var retry []catalogRestore
	for index, item := range batch {
		if time.Now().After(deadline) {
			retry = append(retry, batch[index:]...)
			break
		}
		if _, live := seen[item.id]; !live && !d.restoreEndedSession(item, publish) {
			retry = append(retry, item)
		}
	}
	if len(retry) > 0 {
		d.catalogEnded.setBacklog(kind, append(retry, d.catalogEnded.takeBacklog(kind, math.MaxInt)...))
	}
}

func (d *Daemon) restoreEndedSession(item catalogRestore, publish func(ended, read surface.Session) bool) bool {
	if _, found, err := d.Registry.CatalogProjectionFingerprint(item.id); err != nil || found {
		return true
	}
	session, err := d.Registry.Session(item.id)
	if err != nil {
		return true
	}
	ended := *session
	ended.Status, ended.PID, ended.PIDIdentity, ended.Bridge = surface.StatusOffline, 0, "", ""
	if item.changed.After(ended.LastActive) {
		ended.LastActive = item.changed
	}
	return publish(ended, *session)
}

// presentUnlisted gives a session no discovery pass lists, such as one whose
// start never opened it, the catalog row that listings and archive state read.
func (d *Daemon) presentUnlisted(ctx context.Context, session *surface.Session) error {
	if _, listed, err := d.Registry.CatalogProjectionFingerprint(session.ID); err != nil || listed {
		return err
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return err
	}
	config, err := dashconfig.Load()
	if err != nil {
		return err
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		return err
	}
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		return err
	}
	alias, _ := d.Registry.ReverseAlias(session.ID)
	observedAt := time.Now().UTC()
	d.restoreEndedSession(catalogRestore{id: session.ID}, func(ended, read surface.Session) bool {
		identityCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		identity := d.catalogIdentities.Identity(identityCtx, ended, observedAt, func() *projectresolve.CatalogProject { return d.storedCatalogProject(ended.ID) })
		restore := &registry.SessionUpdate{From: read, Owns: registry.SessionActivityFields | registry.SessionProcessFields, Guard: registry.AllSessionFields}
		return d.publishCatalogSession(ctx, adapter, ended, restore, identity, alias, counts[ended.ID], d.limitResumeRow(ended.ID), false, config, view, nil, observedAt, nil, nil)
	})
	return d.catalog.flushCommitted()
}

func (d *Daemon) discoverEndedSessions(ctx context.Context, adapter surface.Surface, keeper surface.EndedConversations, seen, hidden map[string]struct{}, observedAt time.Time, publish func(ended surface.Session, read *surface.Session, identity projectresolve.CatalogIdentity) bool) error {
	if err := d.recordEndedDiscovery(adapter.Name(), keeper, seen, hidden, observedAt); err != nil {
		return err
	}
	d.restoreEndedSessions(adapter.Name(), keeper, seen, func(session, read surface.Session) bool {
		identityCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return publish(session, &read, d.catalogIdentities.Identity(identityCtx, session, observedAt, func() *projectresolve.CatalogProject { return d.storedCatalogProject(session.ID) }))
	})
	return nil
}

// The archive is the one presence output that reaches a retired row with no session activity.
func (d *Daemon) readCatalogPresence(p *catalogPresence, archive registry.ArchiveState) (registry.CatalogPresenceRead, error) {
	encoded, err := json.Marshal(archive)
	if err != nil {
		return registry.CatalogPresenceRead{}, err
	}
	stamp := string(encoded)
	if stamp != p.archiveStamp {
		read, err := d.Registry.CatalogPresenceRowsWithRetired(p.read, p.hostEpoch, p.clock, p.known)
		if err == nil {
			p.archiveStamp = stamp
		}
		return read, err
	}
	return d.Registry.CatalogPresenceRows(p.read, p.hostEpoch, p.clock, p.known)
}
