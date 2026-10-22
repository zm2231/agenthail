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
		d.runClaudePeerRegistration(ctx, manager.Advertise, func(now time.Time) { manager.RetireInactive(now, surface.ActiveWindow) })
	}()
	return func() { cancel(); <-done; manager.Close() }, nil
}

// runClaudePeerRegistration advertises peers at startup, whenever discovery
// commits a session change, and every 30 seconds so idle peers age out.
func (d *Daemon) runClaudePeerRegistration(ctx context.Context, advertise func(context.Context, []surface.Session) map[string]error, retire func(time.Time)) {
	changed, watching := d.watchCatalogSessions(ctx)
	defer func() { <-watching }()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		d.registerRecentClaudePeers(ctx, advertise)
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
// subscribes before returning, and resubscribes before signalling a follower
// that fell behind the journal, so no commit falls between a pass and the
// subscription. The second channel closes once the watch has ended with ctx.
func (d *Daemon) watchCatalogSessions(ctx context.Context) (<-chan struct{}, <-chan struct{}) {
	changed := make(chan struct{}, 1)
	watching := make(chan struct{})
	signal := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	follower := d.subscribeCatalogSessions(ctx, signal)
	go func() {
		defer close(watching)
		for follower != nil {
			select {
			case <-ctx.Done():
				follower.Cancel()
				return
			case <-follower.Wake:
				for {
					events, err := follower.read(catalogStreamBatch)
					if err != nil {
						follower.Cancel()
						follower = d.subscribeCatalogSessions(ctx, signal)
						signal()
						break
					}
					for _, event := range events {
						if strings.HasPrefix(event.Type, "session.") {
							signal()
							break
						}
					}
					if len(events) < catalogStreamBatch {
						break
					}
				}
			}
		}
	}()
	return changed, watching
}

func (d *Daemon) subscribeCatalogSessions(ctx context.Context, signal func()) *catalogFollower {
	for ctx.Err() == nil {
		_, latest, err := d.Registry.CatalogState()
		if err == nil {
			var window registry.CatalogEventWindow
			var follower *catalogFollower
			window, follower, err = d.catalog.subscribe(latest)
			if err == nil {
				if len(window.Events) > 0 {
					signal()
				}
				return follower
			}
		}
		d.logRuntimeError("claude-peers:catalog", err)
		select {
		case <-ctx.Done():
		case <-time.After(catalogDiscoveryInterval):
		}
	}
	return nil
}

// registerRecentClaudePeers reads the catalog that discovery commits instead
// of listing surfaces again, so a failing surface is listed once per
// discovery pass and stale rows are not proxied.
func (d *Daemon) registerRecentClaudePeers(ctx context.Context, advertise func(context.Context, []surface.Session) map[string]error) {
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
	var eligible []surface.Session
	for _, record := range snapshot.Sessions {
		session := record.Session
		if record.Freshness.Stale || !surface.RecentlyActive(session, now) {
			continue
		}
		// A paired host's sessions, Claude included, have no socket on this
		// machine, so each one gets a peer while its host is connected.
		// Native Claude sessions here publish their own records and sockets.
		if record.HostID != "" {
			if !d.hostConnected(record.HostID) {
				continue
			}
		} else if session.Surface == surface.KindClaude || !configured[session.Surface] {
			continue
		}
		eligible = append(eligible, session)
	}
	failures := advertise(ctx, eligible)
	if ctx.Err() != nil {
		return
	}
	for _, session := range eligible {
		if err := failures[session.ID]; err != nil {
			d.logRuntimeError("claude-peers:"+session.ID, err)
		} else {
			d.clearObserveError("claude-peers:" + session.ID)
		}
	}
}
