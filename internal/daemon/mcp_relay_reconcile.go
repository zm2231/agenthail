package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/mcprelay"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

var relaySettingsPoll = time.Minute

type relayKeeper struct {
	home, executable string
	codex            codexsurface.CodexHome
	done             *mcprelay.Settings
}

func (k *relayKeeper) pass(ctx context.Context, d *Daemon) error {
	settings, err := mcprelay.Read(k.home, k.codex)
	if err != nil || k.done != nil && settings.Equal(*k.done) {
		return err
	}
	changes, err := mcprelay.Reconcile(ctx, settings, k.codex, k.executable)
	for _, change := range changes {
		d.log.Printf("MCP relay: %s", change)
	}
	if err != nil {
		return err
	}
	k.done = &settings
	return nil
}

func (d *Daemon) keepMCPRelaysAtStart(home string) {
	d.keepMCPRelays(&relayKeeper{home: home, executable: surface.RelayExecutable(), codex: codexsurface.CodexHomeFromEnv()})
}

// Reconciling on every change of roots, their mcp settings or Codex lets a root or a Codex set up later get the tools without agenthail mcp install.
func (d *Daemon) keepMCPRelays(keeper *relayKeeper) {
	if keeper.executable == "" {
		return
	}
	d.background.start(func(ctx context.Context) {
		ticker := time.NewTicker(relaySettingsPoll)
		defer ticker.Stop()
		var logged string
		for {
			if err := keeper.pass(ctx, d); err == nil {
				logged = ""
			} else if err.Error() != logged {
				logged = err.Error()
				d.log.Printf("MCP relay: %s", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
