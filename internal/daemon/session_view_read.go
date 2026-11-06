package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type presentationClock struct {
	sync.Mutex
	epoch        string
	sequence     uint64
	fingerprints presentationFingerprints
}

func (d *Daemon) readCatalogPresentation(ctx context.Context, request sessionViewRequest) (registry.CatalogPresentation, error) {
	cache := &d.presentation.fingerprints
	load := d.Registry.ReadCurrentCatalogPresentation
	if request.Scope == "all" || request.Scope == "archived" || request.Filters.Query != "" {
		load = d.Registry.ReadCatalogPresentation
	}
	read, err := load(ctx, cache.stale)
	if err != nil {
		return read, err
	}
	if !cache.valid(read) {
		cache.reset(read.HostEpoch)
		if read, err = load(ctx, cache.stale); err != nil {
			return read, err
		}
		cache.epoch = read.HostEpoch
	}
	return read, cache.adopt(read)
}

func (d *Daemon) readSessionView(ctx context.Context, request sessionViewRequest) (SessionViewSnapshot, error) {
	d.presentation.Lock()
	defer d.presentation.Unlock()
	if d.presentation.epoch == "" {
		d.presentation.epoch = uuid.NewString()
	}
	d.presentation.sequence++
	config, err := LoadDashboardConfig()
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	leads, err := d.Registry.LocalLeadSessions()
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	replacedLeads, err := d.Registry.ReplacedLeadTenures()
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	read, err := d.readCatalogPresentation(ctx, request)
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	now := time.Now().UTC()
	result := SessionViewSnapshot{PresentationPosition: PresentationPosition{HostID: d.hostIdentity().ID, Epoch: d.presentation.epoch, Sequence: d.presentation.sequence}, Sessions: []dashboardSession{}, Counts: map[string]int{}}
	result.HostEpoch, result.CatalogSeq = read.HostEpoch, read.CatalogSeq
	internal := map[string]string{}
	rows := make([]dashboardSession, 0, len(read.Rows))
	revision := make([]revisionRow, 0, len(read.Rows))
	for _, record := range read.Rows {
		row, err := presentationRow(record, d.presentation.fingerprints.rows[record.ID])
		if err != nil {
			return SessionViewSnapshot{}, err
		}
		row.ID = hosts.Qualify(record.HostID, record.ID)
		internal[row.ID] = record.ID
		row.Freshness = &record.Freshness
		row.ObservedAt = record.Freshness.ObservedAt
		row.Display = nil
		row.Attention = nil
		if row.Subagent != nil {
			row.Subagent.ParentID = hosts.Qualify(record.HostID, row.Subagent.ParentID)
			row.Subagent.RootID = hosts.Qualify(record.HostID, row.Subagent.RootID)
		}
		if record.HostID == "" {
			host := d.localHost()
			row.Host = &host
			session := surface.Session{ID: record.ID, Surface: row.Surface, Status: row.Status, RuntimeKind: row.RuntimeKind, LastActive: row.LastActive}
			adapter, adapterErr := d.adapters().For(&session)
			if adapterErr == nil {
				row.Current, row.CurrentReason = surface.PresenceFor(adapter, session, surface.PresenceInput{QueueCount: row.QueueCount, Open: row.Open, Now: now, CodexRecentHours: config.CodexRecentHours, Lead: leads[record.ID]})
			}
			if tenures, replaced := replacedLeads[record.ID]; replaced && !leads[record.ID] {
				row.ReplacedLead = tenures
			}
		}
		if limit, ok := read.Limits[record.ID]; ok {
			row.LimitResume = &dashboardLimitResume{State: limit.State, RefusedAt: limit.RefusedAt, ResetsAt: limit.ResetsAt, MoveTo: d.limitMoveOffer(record.ID, now)}
		} else if record.HostID == "" {
			row.LimitResume = nil
		}
		display := sessionDisplay(row, now)
		row.Display = &display
		row.Attention = rowAttention(row, read.Outgoing[record.ID], record.Attention)
		row.InboxSection = sessionInbox(row, now)
		row.Placement = sessionPlacement(row)
		rows = append(rows, row)
		revision = append(revision, newRevisionRow(record, row))
		if record.Retired && !read.History {
			read.Retired = append(slices.Clip(read.Retired), retiredCountOf(row, record))
		}
	}
	plan := newSessionViewPlan(rows, request.Scope, request.Filters, now)
	plan.countRetired(read.Retired, request.Filters, now)
	plan.deadline(read.RefreshAt)
	plan.view.CatalogHostID = result.PresentationPosition.HostID
	for _, row := range rows {
		if row.Surface == surface.KindCodex && !row.LastActive.IsZero() {
			plan.deadline(row.LastActive.Add(time.Duration(config.CodexRecentHours)*time.Hour + time.Nanosecond))
		}
	}
	semantic := plan.view
	semantic.AsOf = time.Time{}
	semantic.RefreshAt = nil
	fingerprint, err := json.Marshal(struct {
		Epoch string
		Seq   uint64
		View  SessionView
		Rows  []revisionRow
	}{read.HostEpoch + ":" + d.presentation.epoch, read.CatalogSeq, semantic, revision})
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	sum := sha256.Sum256(fingerprint)
	plan.view.Revision = hex.EncodeToString(sum[:])
	if request.Revision != "" && request.Revision != plan.view.Revision {
		return SessionViewSnapshot{}, errPresentationChanged
	}
	result.TotalSessions = plan.view.Counts[request.Scope].Sessions
	for scope, count := range plan.view.Counts {
		result.Counts[scope] = count.Families
	}
	var selected []string
	if len(request.IDs) > 0 {
		allowed := map[string]bool{}
		for _, family := range plan.view.Families {
			for _, member := range family.Members {
				allowed[member.ID] = true
			}
		}
		for _, id := range request.IDs {
			if !allowed[id] {
				return SessionViewSnapshot{}, errPresentationMember
			}
			selected = append(selected, id)
		}
		plan.view.Families = []SessionViewFamily{}
	} else {
		families := plan.view.Families
		if request.Filters.Collapsed {
			families = collapsedFamilies(plan.view)
		}
		rest := familiesAfter(families, request.After)
		end := min(request.Limit, len(rest))
		if end < len(rest) {
			result.NextCursor = encodeSessionViewCursor(request, rest[end-1].key)
		}
		plan.view.Families = rest[:end]
		for _, family := range plan.view.Families {
			if !request.Members {
				selected = append(selected, family.RootID)
				continue
			}
			for _, member := range family.Members {
				selected = append(selected, member.ID)
			}
		}
	}
	for _, id := range selected {
		row := *plan.rows[id]
		hydrated := d.presentation.fingerprints.rows[internal[id]].row
		row.Holders, row.Plan, row.Runtime, row.ClaudeConfigRoot = hydrated.Holders, hydrated.Plan, hydrated.Runtime, hydrated.ClaudeConfigRoot
		row.ConfiguredModel, row.PendingModel = hydrated.ConfiguredModel, hydrated.PendingModel
		result.Sessions = append(result.Sessions, row)
	}
	result.View = &plan.view
	return result, nil
}
