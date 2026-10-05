package daemon

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/peerbridge"
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
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			d.registerRecentClaudePeers(ctx, manager.Ensure)
			manager.RetireInactive(time.Now(), claudePeerIdleLifetime)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done; manager.Close() }, nil
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
