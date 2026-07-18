package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const surfaceOperationTimeout = 20 * time.Second

func (d *Daemon) scanAndRelay(ctx context.Context) {
	if expired, err := d.Registry.ExpireMessages(time.Now()); err != nil {
		d.log.Printf("expire queued messages: %s", err)
	} else if expired > 0 {
		d.publishEvent("state.changed", "", map[string]any{"source": "queue-expired", "count": expired})
	}
	for _, adapter := range d.Surfaces {
		ensurer, ok := adapter.(surface.RuntimeEnsurer)
		if !ok {
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		err := ensurer.EnsureRuntime(operationCtx)
		cancel()
		key := "runtime:" + string(adapter.Name())
		if err != nil {
			d.logRuntimeError(key, err)
			continue
		}
		d.clearObserveError(key)
	}
	if removed := d.refreshAndPruneInactiveClaudeRoutes(ctx, time.Now().Add(-time.Hour)); removed > 0 {
		d.publishEvent("state.changed", "", map[string]any{"source": "relay-removed", "count": removed})
	}
	watched, err := d.Registry.WatchedSessions()
	if err != nil {
		d.log.Printf("scan watched sessions: %s", err)
		return
	}
	for _, watchedSession := range watched {
		adapter := d.surfaceForKind(watchedSession.Surface)
		if adapter == nil {
			d.log.Printf("no adapter for watched session %s (%s)", truncate(watchedSession.ID, 16), watchedSession.Surface)
			continue
		}
		session, sessionErr := d.Registry.Session(watchedSession.ID)
		if sessionErr != nil {
			d.log.Printf("load watched session %s: %s", truncate(watchedSession.ID, 16), sessionErr)
			continue
		}
		if relayTargetInactive(session) {
			continue
		}
		d.observeSession(ctx, adapter, session)
	}
}

func (d *Daemon) refreshAndPruneInactiveClaudeRoutes(ctx context.Context, cutoff time.Time) int {
	routes, err := d.Registry.ListRoutes()
	if err != nil || len(routes) == 0 {
		return 0
	}
	for _, adapter := range d.Surfaces {
		if adapter.Name() != surface.KindClaude {
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		sessions, listErr := adapter.List(operationCtx)
		cancel()
		if listErr != nil {
			d.logObserveError("claude:routes", listErr)
			return 0
		}
		for _, session := range sessions {
			if registerErr := d.Registry.RegisterSession(session); registerErr != nil {
				d.log.Printf("register active Claude session: %s", registerErr)
				return 0
			}
		}
	}
	routes, err = d.Registry.ListRoutes()
	if err != nil {
		return 0
	}
	removed := 0
	for _, route := range routes {
		stale := ""
		for _, sessionID := range []string{route.FromSession, route.ToSession} {
			session, sessionErr := d.Registry.Session(sessionID)
			if sessionErr != nil || session.Surface != surface.KindClaude || !relayTargetInactive(session) {
				continue
			}
			old, ageErr := d.Registry.SessionUpdatedBefore(sessionID, cutoff)
			if ageErr == nil && old {
				stale = sessionID
				break
			}
		}
		if stale == "" {
			continue
		}
		if err := d.Registry.RemoveRoute(route.ID); err != nil {
			d.log.Printf("remove inactive relay %d: %s", route.ID, err)
			continue
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "relay-removed", SessionID: route.ToSession, SourceSessionID: route.FromSession, RouteID: route.ID, Error: "Claude Code session did not resume within 1 hour: " + stale})
		removed++
	}
	return removed
}

func (d *Daemon) observeSession(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	observation, err := adapter.Observe(operationCtx, session)
	cancel()
	if err != nil {
		d.logObserveError(session.ID, err)
		return
	}
	d.clearObserveError(session.ID)
	if observation == nil {
		d.log.Printf("observe %s: empty observation", d.resolveDisplay(session.ID))
		return
	}
	session.Status = observation.Status
	previous, found, err := d.Registry.RuntimeState(session.ID)
	if err != nil {
		d.log.Printf("runtime state %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	desktopNotificationMessage := ""
	mobileNotificationMessage := ""
	completionPredatesActiveDelivery := previous.ActiveTurnID != "" && observation.ActiveTurnID == previous.ActiveTurnID
	if found && !completionPredatesActiveDelivery && observation.CompletedTurnID != "" && observation.CompletedTurnID != previous.CompletedTurnID {
		text := ""
		if observation.Reply != nil && observation.Reply.Done && observation.Reply.Error == "" {
			text = observation.Reply.Text
		}
		if text != "" {
			d.fireRelays(session, observation.CompletedTurnID, previous.RelayHops, text)
		}
		if observation.Reply != nil && observation.Reply.Done {
			desktopNotificationMessage = fmt.Sprintf("%s finished", d.resolveDisplay(session.ID))
			mobileNotificationMessage = fmt.Sprintf("%s finished", notificationSurfaceName(session.Surface))
			if observation.Reply.Error != "" {
				desktopNotificationMessage = fmt.Sprintf("%s failed", d.resolveDisplay(session.ID))
				mobileNotificationMessage = fmt.Sprintf("%s failed", notificationSurfaceName(session.Surface))
			}
		}
	}
	if err := d.Registry.SaveRuntimeState(session.ID, *observation); err != nil {
		d.log.Printf("save runtime state %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	changed := !found || previous.LastStatus != observation.Status || previous.ActiveTurnID != observation.ActiveTurnID || previous.CompletedTurnID != observation.CompletedTurnID
	if changed {
		d.publishEvent("session.updated", session.ID, map[string]any{"status": observation.Status, "activeTurnId": observation.ActiveTurnID, "completedTurnId": observation.CompletedTurnID})
	}
	if found && !completionPredatesActiveDelivery && observation.CompletedTurnID != "" && observation.CompletedTurnID != previous.CompletedTurnID {
		d.publishEvent("turn.completed", session.ID, map[string]string{"turnId": observation.CompletedTurnID})
	}
	if desktopNotificationMessage != "" {
		go func(desktopMessage, mobileMessage, sessionID string) {
			if err := Notify("Agenthail", desktopMessage); err != nil {
				d.log.Printf("desktop notification: %s", err)
			}
			notificationCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			d.notifyPairedDevices(notificationCtx, "Agenthail", mobileMessage, sessionID, "turn.completed")
		}(desktopNotificationMessage, mobileNotificationMessage, session.ID)
	}
	if observation.Status == surface.StatusIdle && observation.ActiveTurnID == "" {
		queued := d.Registry.QueueCount(session.ID)
		d.drainMessageQueue(ctx, adapter, session)
		if queued > 0 {
			d.publishEvent("state.changed", session.ID, map[string]string{"source": "queue"})
		}
	}
}

func notificationSurfaceName(kind surface.SurfaceKind) string {
	switch kind {
	case surface.KindClaude:
		return "Claude Code agent"
	case surface.KindCodex:
		return "Codex agent"
	case surface.KindNotion:
		return "Notion agent"
	default:
		return "Agent"
	}
}

func (d *Daemon) surfaceForKind(kind surface.SurfaceKind) surface.Surface {
	for _, adapter := range d.Surfaces {
		if adapter.Name() == kind {
			return adapter
		}
	}
	return nil
}
