package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type presentationClock struct {
	sync.Mutex
	epoch    string
	sequence uint64
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
	now := time.Now().UTC()
	result := SessionViewSnapshot{PresentationPosition: PresentationPosition{HostID: d.hostIdentity().ID, Epoch: d.presentation.epoch, Sequence: d.presentation.sequence}, Sessions: []dashboardSession{}, Counts: map[string]int{}}
	var plan sessionViewPlan
	internal := map[string]string{}
	var selected []string
	full, err := d.Registry.ReadCatalogPresentation(ctx, func(read registry.CatalogPresentation) ([]string, error) {
		result.HostEpoch, result.CatalogSeq = read.HostEpoch, read.CatalogSeq
		rows := make([]dashboardSession, 0, len(read.Rows))
		for _, record := range read.Rows {
			var row dashboardSession
			if err := json.Unmarshal(record.Data, &row); err != nil {
				return nil, fmt.Errorf("read presentation %s: %w", record.ID, err)
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
					row.Current, row.CurrentReason = surface.PresenceFor(adapter, session, surface.PresenceInput{QueueCount: row.QueueCount, Open: row.Open, Now: now, CodexRecentHours: config.CodexRecentHours})
				}
			}
			if limit, ok := read.Limits[record.ID]; ok {
				row.LimitResume = &dashboardLimitResume{State: limit.State, RefusedAt: limit.RefusedAt, ResetsAt: limit.ResetsAt}
			} else if record.HostID == "" {
				row.LimitResume = nil
			}
			display := sessionDisplay(row, now)
			row.Display = &display
			if !row.Archived && display.Kind != "unavailable" {
				if n := read.Outgoing[record.ID]; n > 0 {
					row.Attention = &SessionAttention{Kind: "outgoing", Reason: fmt.Sprintf("%d messages not delivered", n)}
				}
				if display.Kind == "limited" && row.Attention == nil {
					row.Attention = &SessionAttention{Kind: "usageLimit", Reason: display.Detail}
				}
				if record.Attention != "" {
					row.Attention = &SessionAttention{Kind: "incoming", Reason: record.Attention}
				}
				if row.WaitingOn != nil {
					row.Attention = &SessionAttention{Kind: "request", Reason: display.Detail}
				}
			}
			rows = append(rows, row)
		}
		plan = newSessionViewPlan(rows, request.Scope, request.Filters, now)
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
			Rows  []dashboardSession
		}{read.HostEpoch + ":" + d.presentation.epoch, read.CatalogSeq, semantic, rows})
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(fingerprint)
		plan.view.Revision = hex.EncodeToString(sum[:])
		if request.Revision != "" && request.Revision != plan.view.Revision {
			return nil, errPresentationChanged
		}
		if request.Offset > len(plan.view.Families) {
			return nil, errPresentationCursor
		}
		result.TotalSessions = plan.view.Counts[request.Scope].Sessions
		for scope, count := range plan.view.Counts {
			result.Counts[scope] = count.Families
		}
		if len(request.IDs) > 0 {
			allowed := map[string]bool{}
			for _, family := range plan.view.Families {
				for _, member := range family.Members {
					allowed[member.ID] = true
				}
			}
			for _, id := range request.IDs {
				if !allowed[id] {
					return nil, errPresentationMember
				}
				selected = append(selected, id)
			}
			plan.view.Families = []SessionViewFamily{}
		} else {
			end := min(request.Offset+request.Limit, len(plan.view.Families))
			if end < len(plan.view.Families) {
				result.NextCursor = encodeSessionViewCursor(request, plan.view.Revision, end)
			}
			plan.view.Families = plan.view.Families[request.Offset:end]
			for _, family := range plan.view.Families {
				selected = append(selected, family.RootID)
			}
		}
		ids := make([]string, 0, len(selected))
		for _, id := range selected {
			ids = append(ids, internal[id])
		}
		return ids, nil
	})
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	for _, id := range selected {
		row := plan.rows[id]
		var hydrated dashboardSession
		if err := json.Unmarshal(full[internal[id]], &hydrated); err != nil {
			return SessionViewSnapshot{}, err
		}
		row.Holders, row.Plan, row.Runtime, row.ClaudeConfigRoot = hydrated.Holders, hydrated.Plan, hydrated.Runtime, hydrated.ClaudeConfigRoot
		row.ConfiguredModel, row.PendingModel = hydrated.ConfiguredModel, hydrated.PendingModel
		result.Sessions = append(result.Sessions, row)
	}
	result.View = &plan.view
	return result, nil
}
