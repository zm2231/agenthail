package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const surfaceOperationTimeout = 20 * time.Second

func (d *Daemon) scanAndRelay(ctx context.Context) {
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
		d.log.Printf("observe %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
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
	newCompletions := unseenCompletions(observation, previous.CompletedTurnID, found)
	if err := d.Registry.SaveRuntimeState(session.ID, *observation); err != nil {
		d.log.Printf("save runtime state %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	for _, completion := range newCompletions {
		if completion.Reply == nil || !completion.Reply.Done {
			continue
		}
		if completion.Reply.Error == "" && completion.Reply.Text != "" {
			d.fireRelays(session, completion.ID, completion.Reply.Text)
		}
		notification := fmt.Sprintf("%s finished", d.resolveDisplay(session.ID))
		if completion.Reply.Error != "" {
			notification = fmt.Sprintf("%s failed", d.resolveDisplay(session.ID))
		}
		go func(message string) {
			if err := Notify("Agenthail", message); err != nil {
				d.log.Printf("desktop notification: %s", err)
			}
		}(notification)
	}
	if observation.Status == surface.StatusIdle && observation.ActiveTurnID == "" {
		d.drainMessageQueue(ctx, adapter, session)
	}
}

func unseenCompletions(observation *surface.TurnObservation, previousID string, found bool) []surface.Completion {
	if !found || observation == nil || observation.CompletedTurnID == "" {
		return nil
	}
	completions := observation.Completions
	if len(completions) == 0 {
		if observation.CompletedTurnID == previousID {
			return nil
		}
		return []surface.Completion{{ID: observation.CompletedTurnID, Reply: observation.Reply}}
	}
	if previousID == "" {
		return nil
	}
	for i, completion := range completions {
		if completion.ID == previousID {
			return completions[i+1:]
		}
	}
	return completions[len(completions)-1:]
}

func (d *Daemon) surfaceForKind(kind surface.SurfaceKind) surface.Surface {
	for _, adapter := range d.Surfaces {
		if adapter.Name() == kind {
			return adapter
		}
	}
	return nil
}
