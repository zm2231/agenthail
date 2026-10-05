package daemon

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/peerbridge"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const claudePeerIdleLifetime = 24 * time.Hour

func (d *Daemon) startClaudePeers(ctx context.Context) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	manager, err := peerbridge.Start(ctx, home, d.Registry, executable)
	if err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.runClaudePeerRegistration(ctx, manager.Ensure, func(now time.Time) { manager.RetireInactive(now, claudePeerIdleLifetime) })
	}()
	return func() { cancel(); <-done; manager.Close() }, nil
}

// runClaudePeerRegistration registers peers at startup, whenever discovery
// commits a session change, and every 30 seconds so idle peers age out.
func (d *Daemon) runClaudePeerRegistration(ctx context.Context, ensure func(context.Context, string) error, retire func(time.Time)) {
	changed := d.watchCatalogSessions(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		d.registerRecentClaudePeers(ctx, ensure)
		retire(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-changed:
		}
	}
}

// watchCatalogSessions signals after discovery commits a session change, so a
// newly discovered agent becomes a peer without waiting for the next tick. It
// subscribes before returning, and resubscribes before signalling a dropped
// subscription, so no commit falls between a pass and the subscription.
func (d *Daemon) watchCatalogSessions(ctx context.Context) <-chan struct{} {
	changed := make(chan struct{}, 1)
	signal := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	stream, cancel := d.subscribeCatalogSessions(ctx, signal)
	go func() {
		for stream != nil {
			select {
			case <-ctx.Done():
				cancel()
				return
			case event, open := <-stream:
				if !open {
					cancel()
					stream, cancel = d.subscribeCatalogSessions(ctx, signal)
					signal()
				} else if strings.HasPrefix(event.Type, "session.") {
					signal()
				}
			}
		}
	}()
	return changed
}

func (d *Daemon) subscribeCatalogSessions(ctx context.Context, signal func()) (<-chan registry.CatalogEvent, func()) {
	for ctx.Err() == nil {
		_, latest, err := d.Registry.CatalogState()
		if err == nil {
			var window registry.CatalogEventWindow
			var stream <-chan registry.CatalogEvent
			var cancel func()
			window, stream, cancel, err = d.catalog.subscribe(latest)
			if err == nil {
				if len(window.Events) > 0 {
					signal()
				}
				return stream, cancel
			}
		}
		d.logRuntimeError("claude-peers:catalog", err)
		select {
		case <-ctx.Done():
		case <-time.After(catalogDiscoveryInterval):
		}
	}
	return nil, func() {}
}

// registerRecentClaudePeers reads the catalog that discovery commits instead
// of listing surfaces again, so a failing surface is listed once per
// discovery pass and stale rows are not proxied.
func (d *Daemon) registerRecentClaudePeers(ctx context.Context, ensure func(context.Context, string) error) {
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		d.logRuntimeError("claude-peers:catalog", err)
		return
	}
	configured := make(map[surface.SurfaceKind]bool, len(d.Surfaces))
	for _, adapter := range d.Surfaces {
		configured[adapter.Name()] = true
	}
	now := time.Now()
	for _, record := range snapshot.Sessions {
		session := record.Session
		// Native Claude sessions publish their own records and socket endpoints.
		if session.Surface == surface.KindClaude || !configured[session.Surface] || record.Freshness.Stale || !claudePeerEligible(session, now) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return
		}
		operationCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		err := ensure(operationCtx, session.ID)
		cancel()
		if err != nil {
			d.logRuntimeError("claude-peers:"+session.ID, err)
		} else {
			d.clearObserveError("claude-peers:" + session.ID)
		}
	}
}

func claudePeerEligible(session surface.Session, now time.Time) bool {
	if session.ID == "" || session.Status == surface.StatusOffline {
		return false
	}
	if session.Status == surface.StatusBusy {
		return true
	}
	return !session.LastActive.IsZero() && now.Sub(session.LastActive) <= claudePeerIdleLifetime
}
