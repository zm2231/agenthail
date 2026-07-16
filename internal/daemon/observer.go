package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const surfaceOperationTimeout = 20 * time.Second

func (d *Daemon) scanAndRelay(ctx context.Context) {
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
		d.observeSession(ctx, adapter, session)
	}
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
	if found && observation.CompletedTurnID != "" && observation.CompletedTurnID != previous.CompletedTurnID {
		text := ""
		if observation.Reply != nil && observation.Reply.Done && observation.Reply.Error == "" {
			text = observation.Reply.Text
		}
		if text != "" {
			d.fireRelays(session, observation.CompletedTurnID, previous.RelayHops, text)
		}
		if observation.Reply != nil && observation.Reply.Done {
			desktopNotificationMessage = fmt.Sprintf("%s finished", d.resolveDisplay(session.ID))
			mobileNotificationMessage = "An agent finished"
			if observation.Reply.Error != "" {
				desktopNotificationMessage = fmt.Sprintf("%s failed", d.resolveDisplay(session.ID))
				mobileNotificationMessage = "An agent failed"
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
	if found && observation.CompletedTurnID != "" && observation.CompletedTurnID != previous.CompletedTurnID {
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

func (d *Daemon) surfaceForKind(kind surface.SurfaceKind) surface.Surface {
	for _, adapter := range d.Surfaces {
		if adapter.Name() == kind {
			return adapter
		}
	}
	return nil
}
