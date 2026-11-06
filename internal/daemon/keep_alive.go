package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	keepAliveEvery   = 30 * time.Second
	keepAliveWindow  = 30 * time.Minute
	keepAliveHealthy = 10 * time.Minute
)

var keepAliveBackoff = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}

func (d *Daemon) runKeepAlive(ctx context.Context) {
	ticker := time.NewTicker(keepAliveEvery)
	defer ticker.Stop()
	d.keepPinned(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.keepPinned(ctx, now)
		}
	}
}

// keepPinned brings back the pinned sessions of this host whose conversation
// ended. It only opens a conversation, so no turn runs and no tokens are spent;
// a paired host keeps its own.
func (d *Daemon) keepPinned(ctx context.Context, now time.Time) {
	pinned, err := d.Registry.PinnedSessions()
	if err != nil {
		d.log.Printf("read pinned sessions: %s", err)
		return
	}
	for _, listed := range pinned {
		if ctx.Err() != nil {
			return
		}
		d.keepSession(ctx, listed.Session.ID, now)
	}
}

// keepSession holds the session's keep lock, which pin, unpin, stop and
// resume also take, so what it reads stays true until it has written.
func (d *Daemon) keepSession(ctx context.Context, sessionID string, now time.Time) {
	unlock, ok := d.keepLocks.acquire(sessionID, false)
	if !ok {
		return
	}
	defer unlock()
	p, found, err := d.Registry.PinnedSession(sessionID)
	if err != nil || !found {
		return
	}
	session := p.Session
	if daemonOwned, err := d.Registry.StartedByDaemon(session.ID); err != nil || daemonOwned {
		return
	}
	adapter, err := d.adapters().For(&session)
	if err != nil {
		return
	}
	keeper, ok := adapter.(surface.Keeper)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	state := p.KeepAlive
	next := state
	if !keeper.Ended(ctx, &session) {
		next = keptHealthy(state, now)
	} else if state.FailedAt.IsZero() && !now.Before(state.NextAttempt) {
		addressed := session
		wakeErr := keeper.Wake(ctx, &session)
		if errors.Is(wakeErr, surface.ErrDeliveryHeld) {
			next = keptHealthy(state, now)
		} else {
			next = keptAttempt(state, wakeErr, now)
		}
		if wakeErr == nil {
			if err := d.dispatcher().RegisterReached(&addressed, &session); err != nil {
				d.log.Printf("record kept session %s: %s", d.resolveDisplay(session.ID), err)
			}
			d.log.Printf("brought back pinned session %s", d.resolveDisplay(session.ID))
		} else if !errors.Is(wakeErr, surface.ErrDeliveryHeld) {
			d.log.Printf("bring back pinned session %s: %s", d.resolveDisplay(session.ID), wakeErr)
		}
	}
	if next == state {
		return
	}
	if err := d.Registry.SaveKeepAlive(session.ID, next); err != nil {
		d.log.Printf("record keeping %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if state.FailedAt.IsZero() != next.FailedAt.IsZero() {
		d.publishEvent("state.changed", session.ID, map[string]string{"source": "keep-alive"})
		d.requestCatalogDiscovery()
	}
	if !next.FailedAt.IsZero() && !next.Announced {
		d.announceKeepAliveFailed(session, next)
	}
}

func keptHealthy(state registry.KeepAlive, now time.Time) registry.KeepAlive {
	if state == (registry.KeepAlive{}) {
		return state
	}
	if state.HealthySince.IsZero() {
		state.HealthySince = now
		return state
	}
	if now.Sub(state.HealthySince) >= keepAliveHealthy {
		return registry.KeepAlive{}
	}
	return state
}

// keptAttempt counts a wake, whether it failed or the session ended again
// soon after, so a session that keeps ending stops being brought back.
func keptAttempt(state registry.KeepAlive, err error, now time.Time) registry.KeepAlive {
	if state.FirstAttempt.IsZero() || now.Sub(state.FirstAttempt) > keepAliveWindow {
		state = registry.KeepAlive{FirstAttempt: now}
	}
	state.Attempts++
	state.HealthySince = time.Time{}
	state.Error = ""
	if err != nil {
		state.Error = err.Error()
	}
	if state.Attempts >= len(keepAliveBackoff) {
		state.FailedAt = now
		state.NextAttempt = time.Time{}
		return state
	}
	state.NextAttempt = now.Add(keepAliveBackoff[state.Attempts-1])
	return state
}
