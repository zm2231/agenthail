package daemon

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"sync"
	"time"

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
	mu        sync.Mutex
	rechecked map[surface.SurfaceKind]time.Time
	backlog   map[surface.SurfaceKind][]catalogRestore
}

func (e *catalogEnded) recheckDue(kind surface.SurfaceKind, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rechecked == nil {
		e.rechecked = map[surface.SurfaceKind]time.Time{}
	}
	if at, found := e.rechecked[kind]; found && now.Sub(at) < catalogEndedRecheck {
		return false
	}
	e.rechecked[kind] = now
	return true
}

func (e *catalogEnded) setBacklog(kind surface.SurfaceKind, items []catalogRestore) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backlog == nil {
		e.backlog = map[surface.SurfaceKind][]catalogRestore{}
	}
	e.backlog[kind] = items
}

func (e *catalogEnded) takeBacklog(kind surface.SurfaceKind, limit int) []catalogRestore {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := e.backlog[kind]
	limit = min(limit, len(items))
	e.backlog[kind] = items[limit:]
	return items[:limit]
}

// Kept rows are statted only on a recheck, so a pass costs the same however many there are.
func (d *Daemon) recordEndedDiscovery(kind surface.SurfaceKind, keeper surface.EndedConversations, seen map[string]struct{}, observedAt time.Time) error {
	recheck := d.catalogEnded.recheckDue(kind, observedAt)
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

func (d *Daemon) restoreEndedSessions(kind surface.SurfaceKind, keeper surface.EndedConversations, seen map[string]struct{}, publish func(surface.Session)) {
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
	for index, item := range batch {
		if time.Now().After(deadline) {
			d.catalogEnded.setBacklog(kind, append(slices.Clone(batch[index:]), d.catalogEnded.takeBacklog(kind, math.MaxInt)...))
			return
		}
		if _, live := seen[item.id]; !live {
			d.restoreEndedSession(item, publish)
		}
	}
}

func (d *Daemon) restoreEndedSession(item catalogRestore, publish func(surface.Session)) {
	if _, found, err := d.Registry.CatalogProjectionFingerprint(item.id); err != nil || found {
		return
	}
	session, err := d.Registry.Session(item.id)
	if err != nil {
		return
	}
	ended := *session
	ended.Status, ended.PID, ended.Bridge = surface.StatusOffline, 0, ""
	if item.changed.After(ended.LastActive) {
		ended.LastActive = item.changed
	}
	publish(ended)
}

func (d *Daemon) discoverEndedSessions(ctx context.Context, adapter surface.Surface, keeper surface.EndedConversations, seen map[string]struct{}, observedAt time.Time, publish func(surface.Session, catalogIdentity)) error {
	if err := d.recordEndedDiscovery(adapter.Name(), keeper, seen, observedAt); err != nil {
		return err
	}
	d.restoreEndedSessions(adapter.Name(), keeper, seen, func(session surface.Session) {
		identityCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		publish(session, d.catalogIdentities.identity(identityCtx, session, observedAt, func() *CatalogProject { return d.storedCatalogProject(session.ID) }))
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
