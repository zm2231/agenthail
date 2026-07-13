package daemon

import (
	"context"
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
	if found && observation.CompletedTurnID != "" && observation.CompletedTurnID != previous.CompletedTurnID {
		text := ""
		if observation.Reply != nil && observation.Reply.Done && observation.Reply.Error == "" {
			text = observation.Reply.Text
		}
		if text != "" {
			d.fireRelays(session, observation.CompletedTurnID, previous.RelayHops, text)
		}
	}
	if err := d.Registry.SaveRuntimeState(session.ID, *observation); err != nil {
		d.log.Printf("save runtime state %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if observation.Status == surface.StatusIdle && observation.ActiveTurnID == "" {
		d.drainMessageQueue(ctx, adapter, session)
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
