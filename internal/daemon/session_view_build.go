package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type sessionViewCacheKey struct {
	scope            string
	filters          SessionViewFilters
	catalog          registry.CatalogPresentationKey
	codexRecentHours int
	host             dashboardHost
	epoch            string
}

func (d *Daemon) sessionViewCacheKeyFor(request sessionViewRequest, catalog registry.CatalogPresentationKey, config DashboardConfig) sessionViewCacheKey {
	return sessionViewCacheKey{scope: request.Scope, filters: request.Filters, catalog: catalog, codexRecentHours: config.CodexRecentHours, host: d.localHost(), epoch: d.presentation.epoch}
}

type sessionViewBuild struct {
	key       sessionViewCacheKey
	until     time.Time
	hostEpoch string
	seq       uint64
	view      SessionView
	rows      map[string]*dashboardSession
}

const sessionViewCacheSize = 16

func (b *sessionViewBuild) fresh(key sessionViewCacheKey, now time.Time) bool {
	return b.key == key && (b.until.IsZero() || now.Before(b.until))
}

func (d *Daemon) cachedSessionView(ctx context.Context, request sessionViewRequest, now time.Time) (*sessionViewBuild, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return nil, err
	}
	catalog, err := d.Registry.ReadCatalogPresentationKey(ctx)
	if err != nil {
		return nil, err
	}
	key := d.sessionViewCacheKeyFor(request, catalog, config)
	views := &d.presentation.views
	for i, build := range *views {
		if build.fresh(key, now) {
			*views = append(append([]*sessionViewBuild{build}, (*views)[:i]...), (*views)[i+1:]...)
			d.presentation.hits++
			return build, nil
		}
	}
	build, err := d.buildSessionView(ctx, request, config, key, now)
	if err != nil {
		return nil, err
	}
	d.presentation.builds++
	*views = slices.DeleteFunc(*views, func(old *sessionViewBuild) bool { return old.key.catalog != build.key.catalog })
	*views = append([]*sessionViewBuild{build}, *views...)
	if len(*views) > sessionViewCacheSize {
		*views = (*views)[:sessionViewCacheSize]
	}
	return build, nil
}

func (d *Daemon) buildSessionView(ctx context.Context, request sessionViewRequest, config DashboardConfig, key sessionViewCacheKey, now time.Time) (*sessionViewBuild, error) {
	leads, err := d.Registry.LocalLeadSessions()
	if err != nil {
		return nil, err
	}
	replacedLeads, err := d.Registry.ReplacedLeadTenures()
	if err != nil {
		return nil, err
	}
	if d.presentation.building != nil {
		d.presentation.building()
	}
	read, err := d.readCatalogPresentation(ctx, request)
	if err != nil {
		return nil, err
	}
	build := &sessionViewBuild{key: key, hostEpoch: read.HostEpoch, seq: read.CatalogSeq}
	host := build.key.host
	internal := map[string]string{}
	rows := make([]dashboardSession, 0, len(read.Rows))
	revision := make([]revisionRow, 0, len(read.Rows))
	for _, record := range read.Rows {
		row, err := presentationRow(record, d.presentation.fingerprints.rows[record.ID])
		if err != nil {
			return nil, err
		}
		row.ID = hosts.Qualify(record.HostID, record.ID)
		internal[row.ID] = record.ID
		row.Freshness = &registry.CatalogFreshness{Generation: record.Freshness.Generation, Stale: record.Freshness.Stale}
		row.ObservedAt = time.Time{}
		row.Display = nil
		row.Attention = nil
		if row.Subagent != nil {
			row.Subagent.ParentID = hosts.Qualify(record.HostID, row.Subagent.ParentID)
			row.Subagent.RootID = hosts.Qualify(record.HostID, row.Subagent.RootID)
		}
		if record.HostID == "" {
			local := host
			row.Host = &local
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
			if limit.State == registry.LimitResumeWaiting {
				build.expire(now.Add(limitMoveOfferTTL))
			}
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
	plan.view.CatalogHostID = host.ID
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
	}{read.HostEpoch + ":" + build.key.epoch, read.CatalogSeq, semantic, revision})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(fingerprint)
	plan.view.Revision = hex.EncodeToString(sum[:])
	for id, row := range plan.rows {
		hydrated := d.presentation.fingerprints.rows[internal[id]].row
		row.Holders, row.Plan, row.Runtime, row.ClaudeConfigRoot = hydrated.Holders, hydrated.Plan, hydrated.Runtime, hydrated.ClaudeConfigRoot
		row.ConfiguredModel, row.PendingModel = hydrated.ConfiguredModel, hydrated.PendingModel
	}
	if plan.view.RefreshAt != nil {
		build.expire(*plan.view.RefreshAt)
	}
	build.view, build.rows = plan.view, plan.rows
	return build, nil
}

func (b *sessionViewBuild) expire(at time.Time) {
	if b.until.IsZero() || at.Before(b.until) {
		b.until = at
	}
}
