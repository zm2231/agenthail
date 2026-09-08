package daemon

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/peerbridge"
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
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			d.registerRecentClaudePeers(ctx, manager.Ensure)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done; manager.Close() }, nil
}

func (d *Daemon) registerRecentClaudePeers(ctx context.Context, ensure func(context.Context, string) error) {
	for _, adapter := range d.Surfaces {
		// Native Claude sessions publish their own records and socket endpoints.
		if adapter.Name() == surface.KindClaude {
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		sessions, err := adapter.List(operationCtx)
		cancel()
		if err != nil {
			d.logRuntimeError("claude-peers:"+string(adapter.Name()), err)
			continue
		}
		for _, session := range sessions {
			if session.ID == "" || session.Status == surface.StatusOffline {
				continue
			}
			if err := ctx.Err(); err != nil {
				return
			}
			if err := d.Registry.RegisterSession(session); err != nil {
				d.logRuntimeError("claude-peers:registry", err)
				continue
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
}
