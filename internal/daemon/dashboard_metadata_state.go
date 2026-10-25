package daemon

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardMetadataState(config DashboardConfig, surfaces []registry.CatalogSurfaceState) (dashboardState, error) {
	now := time.Now()
	eventCursor := uint64(0)
	if d.events != nil {
		eventCursor = d.events.cursor()
	}
	queue, err := d.Registry.ListQueue(false, d.adapters())
	if err != nil {
		return dashboardState{}, fmt.Errorf("read queue: %w", err)
	}
	channels, err := d.Registry.ListChannels()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read channels: %w", err)
	}
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read relays: %w", err)
	}
	history, err := d.Registry.ListHistory(50, "")
	if err != nil {
		return dashboardState{}, fmt.Errorf("read delivery history: %w", err)
	}
	attention, err := d.Registry.ListAttentionItems(false)
	if err != nil {
		return dashboardState{}, fmt.Errorf("read attention items: %w", err)
	}
	deliveryProblems, err := d.Registry.ListDeliveryProblems()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read delivery problems: %w", err)
	}
	for index := range deliveryProblems {
		deliveryProblems[index].SessionID = d.publicSessionID(deliveryProblems[index].SessionID)
		deliveryProblems[index].SourceSessionID = d.publicSessionID(deliveryProblems[index].SourceSessionID)
		if deliveryProblems[index].AttentionSessionID != "" {
			deliveryProblems[index].AttentionSessionID = d.publicSessionID(deliveryProblems[index].AttentionSessionID)
		}
	}
	rekeyed, err := d.Registry.SessionRekeys()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read session rekeys: %w", err)
	}
	coordinators, err := d.Registry.LocalCoordinators()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read coordinators: %w", err)
	}
	person, err := d.person()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read the person's name: %w", err)
	}
	state := dashboardState{UpdatedAt: now.UTC(), EventCursor: eventCursor, Daemon: map[string]any{"running": true, "pid": os.Getpid(), "host": d.localHost(), "hosts": d.hostSummaries()}, Surfaces: make([]dashboardSurface, 0, len(d.Surfaces)), Sessions: []dashboardSession{}, Queue: make([]dashboardQueue, 0, len(queue)), Channels: make([]dashboardChannel, 0, len(channels)), Relays: make([]dashboardRelay, 0, len(routes)), History: make([]dashboardHistory, 0, len(history)), Attention: make([]dashboardAttention, 0, len(attention)), DeliveryProblems: deliveryProblems, CodexRecentHours: config.CodexRecentHours, BusyDelivery: string(config.BusyDelivery), Signin: make([]signin.Status, 0, len(signin.Consumers)), Rekeyed: rekeyed, Person: person}
	for _, consumer := range signin.Consumers {
		state.Signin = append(state.Signin, d.signinConsumer(consumer))
	}
	state.Coordinators = make([]registry.CoordinatorEntry, 0, len(coordinators))
	for _, spec := range coordinators {
		state.Coordinators = append(state.Coordinators, spec.Entry())
	}
	for _, item := range queue {
		state.Queue = append(state.Queue, d.dashboardQueueItem(item))
	}
	for _, channel := range channels {
		members := make([]string, 0, len(channel.Members))
		memberDetails := make([]dashboardChannelMember, 0, len(channel.Members))
		for _, member := range channel.Members {
			display := d.resolveDisplay(member)
			members = append(members, display)
			memberDetails = append(memberDetails, dashboardChannelMember{ID: member, Display: display})
		}
		state.Channels = append(state.Channels, dashboardChannel{Name: channel.Name, Members: members, MemberDetails: memberDetails})
	}
	for _, route := range routes {
		state.Relays = append(state.Relays, dashboardRelay{ID: route.ID, From: d.resolveDisplay(route.FromSession), To: d.resolveDisplay(route.ToSession), Pattern: route.Pattern, Handoff: route.Handoff, EventCursor: route.EventCursor, PendingCount: route.PendingCount, LastError: route.LastError, Once: route.Once, Active: route.Active, FireCount: route.FireCount, LastFiredAt: route.LastFiredAt})
	}
	for _, entry := range history {
		state.History = append(state.History, d.dashboardHistoryEntry(entry))
	}
	for _, item := range attention {
		state.Attention = append(state.Attention, dashboardAttention{ID: item.ID, SessionID: d.publicSessionID(item.SessionID), Target: d.resolveDisplay(item.SessionID), QueueID: item.QueueID, Reason: item.Reason, RequestedAction: item.RequestedAction, CreatedAt: item.CreatedAt})
	}
	catalogSurface := make(map[surface.SurfaceKind]registry.CatalogSurfaceState, len(surfaces))
	for _, record := range surfaces {
		catalogSurface[record.Surface] = record
	}
	for _, adapter := range d.Surfaces {
		entry := dashboardSurface{Name: string(adapter.Name()), Connected: false, Health: "unknown", Capabilities: adapter.Capabilities()}
		if record, found := catalogSurface[adapter.Name()]; found {
			entry.Health = record.Health
			entry.HealthDetail = record.Detail
			entry.Connected = record.Health == "healthy"
		}
		if observed, found := d.observedSurfaceHealth(adapter.Name()); found {
			entry.Health = observed.Health
			entry.HealthDetail = observed.HealthDetail
			entry.Runtime = observed.Runtime
			entry.RepairAction = observed.RepairAction
			entry.RepairLabel = observed.RepairLabel
		}
		state.Surfaces = append(state.Surfaces, entry)
	}
	sort.Slice(state.Surfaces, func(i, j int) bool { return state.Surfaces[i].Name < state.Surfaces[j].Name })
	return state, nil
}
