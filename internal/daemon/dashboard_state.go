package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardQueueItem(item registry.QueueRow) dashboardQueue {
	return dashboardQueue{TurnOptions: item.TurnOptions, ID: item.ID, SessionID: d.publicSessionID(item.SessionID), SourceSessionID: d.publicSessionID(item.SourceSessionID), Target: d.resolveDisplay(item.SessionID), Message: item.Message, Model: item.Model, Status: item.Status, Attempts: item.Attempts, LastError: item.LastError, QueuedAt: item.QueuedAt, HeldBy: item.HeldBy, Historical: item.Historical, Evidence: item.Evidence, Operation: item.Operation, BusyDelivery: item.BusyDelivery}
}

func (d *Daemon) dashboardState(ctx context.Context, pageRequest ...registry.CatalogPageRequest) (dashboardState, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return dashboardState{}, fmt.Errorf("load dashboard config: %w", err)
	}
	var catalogSnapshot registry.CatalogSnapshot
	var page *registry.CatalogPage
	if len(pageRequest) > 0 {
		request := pageRequest[0]
		loaded, err := d.Registry.CatalogSnapshotPage(request)
		if err != nil {
			return dashboardState{}, fmt.Errorf("read catalog page: %w", err)
		}
		catalogSnapshot = loaded.CatalogSnapshot
		page = &loaded
	} else {
		catalogSnapshot, err = d.Registry.CatalogSnapshot()
		if err != nil {
			return dashboardState{}, fmt.Errorf("read catalog state: %w", err)
		}
	}
	now := time.Now()
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read queue counts: %w", err)
	}
	limits, err := d.limitResumeRows()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read usage-limit stops: %w", err)
	}
	aliases, err := d.Registry.ListAliases()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read aliases: %w", err)
	}
	aliasByID := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		aliasByID[alias.SessionID] = alias.Name
	}
	state, err := d.dashboardMetadataState(config, catalogSnapshot.Surfaces)
	if err != nil {
		return dashboardState{}, err
	}
	state.HostEpoch, state.CatalogSeq = catalogSnapshot.HostEpoch, catalogSnapshot.CatalogSeq
	if page != nil {
		state.catalogPageApplied = true
		state.catalogPageFilter = pageRequest[0].Scope + "\x00" + pageRequest[0].ProjectID + "\x00" + pageRequest[0].Query + "\x00" + pageRequest[0].ParentID
		state.catalogPageOffset = page.Offset
		state.catalogPageLimit = page.Limit
		state.catalogPageHasMore = page.HasMore
		state.UpdatedAt = pageRequest[0].Now.UTC()
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		return dashboardState{}, err
	}
	projectRecords, err := d.Registry.ListProjects()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read projects: %w", err)
	}
	catalogSessions := map[string]registry.CatalogSessionState{}
	sessions := make([]surface.Session, 0, len(catalogSnapshot.Sessions))
	mirror := catalogMirror(ctx)
	for _, record := range catalogSnapshot.Sessions {
		if !mirror && (catalogSnapshot.Listing.Hidden[record.Session.ID] || page == nil && catalogSnapshot.Listing.Retired[record.Session.ID]) {
			continue
		}
		catalogSessions[record.Session.ID] = record
		sessions = append(sessions, record.Session)
	}
	if page == nil && len(catalogSnapshot.Sessions) == 0 && len(catalogSnapshot.Surfaces) == 0 {
		var err error
		sessions, err = d.Registry.ListSessions(0)
		if err != nil {
			return dashboardState{}, fmt.Errorf("read session catalog: %w", err)
		}
	}
	local := make([]int, 0, len(sessions))
	for _, session := range sessions {
		if record, found := catalogSessions[session.ID]; found && record.HostID != "" {
			var mirrored dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &mirrored) != nil {
				continue
			}
			mirrored.ObservedAt = record.ObservedAt
			mirrored.Freshness = &record.Freshness
			mirrored.HiddenSubagents, mirrored.LiveHiddenSubagents = catalogSnapshot.Listing.HiddenSubagents(session.ID), catalogSnapshot.Listing.LiveHiddenSubagents(session.ID)
			mirrored.Subagents = addSubagentRollups(mirrored.ObservedSubagents, mirrored.LiveHiddenSubagents)
			state.Sessions = append(state.Sessions, mirrored)
			continue
		}
		adapter, adapterErr := d.adapters().For(&session)
		if adapterErr != nil {
			continue
		}
		identity := catalogIdentity{}
		observedAt := time.Time{}
		if record, found := catalogSessions[session.ID]; found {
			_ = json.Unmarshal(record.Project, &identity.Project)
			_ = json.Unmarshal(record.Checkout, &identity.Checkout)
			identity.UnavailableReason = record.UnavailableReason
			observedAt = record.ObservedAt
		}
		if observedAt.IsZero() {
			observedAt = now.UTC()
		}
		entry := d.catalogSessionProjection(adapter, session, identity, observedAt, aliasByID[session.ID], counts[session.ID], limits[session.ID], false, config)
		if record, found := catalogSessions[session.ID]; found {
			entry.Freshness = &record.Freshness
		}
		entry.QueueCount = counts[session.ID]
		if record, found := catalogSessions[session.ID]; found && record.ProjectionFingerprint != "" {
			var saved dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &saved) == nil {
				entry.Open, entry.Listed = saved.Open, saved.Listed
				entry.ObservedSubagents = saved.ObservedSubagents
				entry.Current, entry.CurrentReason = saved.Current, saved.CurrentReason
				entry.Holders = saved.Holders
				entry.Activity = saved.Activity
				entry.Plan = saved.Plan
			}
		}
		entry.HiddenSubagents, entry.LiveHiddenSubagents = catalogSnapshot.Listing.HiddenSubagents(session.ID), catalogSnapshot.Listing.LiveHiddenSubagents(session.ID)
		entry.Subagents = addSubagentRollups(entry.ObservedSubagents, entry.LiveHiddenSubagents)
		local = append(local, len(state.Sessions))
		state.Sessions = append(state.Sessions, entry)
	}
	localRows := make([]dashboardSession, 0, len(local))
	for _, index := range local {
		localRows = append(localRows, state.Sessions[index])
	}
	d.settleArchiveActivity(localRows, view)
	for _, index := range local {
		applyCatalogView(&state.Sessions[index], view)
	}
	sort.Slice(state.Surfaces, func(i, j int) bool { return state.Surfaces[i].Name < state.Surfaces[j].Name })
	if page == nil {
		sort.Slice(state.Sessions, func(i, j int) bool {
			if state.Sessions[i].Status == surface.StatusBusy && state.Sessions[j].Status != surface.StatusBusy {
				return true
			}
			if state.Sessions[j].Status == surface.StatusBusy && state.Sessions[i].Status != surface.StatusBusy {
				return false
			}
			return state.Sessions[i].LastActive.After(state.Sessions[j].LastActive)
		})
	}
	if page != nil {
		state.TotalSessions = page.TotalMatching
	} else {
		state.TotalSessions = len(state.Sessions)
		state.Counts = catalogScopeCounts(state.Sessions)
		coordinators, err := d.Registry.Coordinators()
		if err != nil {
			return dashboardState{}, fmt.Errorf("read coordinators: %w", err)
		}
		state.Projects = catalogProjects(state.Sessions, projectRecords, coordinators, view.archive.Projects, d.localHost(), d.coordinatorHostRef)
	}
	return state, nil
}
