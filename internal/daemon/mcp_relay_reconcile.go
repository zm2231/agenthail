package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/claudemcp"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

var relaySettingsPoll = time.Minute

type claudeRelayConfig struct {
	path string
	off  bool
}

type relaySettings struct {
	claude     []claudeRelayConfig
	codexSetUp bool
	codexOff   bool
}

func (s relaySettings) equal(other relaySettings) bool {
	return slices.Equal(s.claude, other.claude) && s.codexSetUp == other.codexSetUp && s.codexOff == other.codexOff
}

func readRelaySettings(home string, codex codexsurface.CodexHome) (relaySettings, error) {
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return relaySettings{}, err
	}
	var settings relaySettings
	for _, root := range roots {
		// A root Claude has not created yet would start signed out; its config is not ours to create.
		if _, err := os.Stat(root); err != nil {
			continue
		}
		off, err := surface.ClaudeRootMCPOff(home, root)
		if err != nil {
			return relaySettings{}, err
		}
		settings.claude = append(settings.claude, claudeRelayConfig{path: surface.ClaudeGlobalConfigPath(home, root), off: off})
	}
	settings.codexSetUp = codexsurface.CodexSetUp(codex)
	if settings.codexOff, err = surface.CodexMCPOff(home); err != nil {
		return relaySettings{}, err
	}
	return settings, nil
}

func (d *Daemon) reconcileMCPRelays(ctx context.Context, settings relaySettings, codex codexsurface.CodexHome, executable string) error {
	var failed []error
	for _, config := range settings.claude {
		changed, err := claudemcp.ReconcileAt(config.path, executable, config.off)
		switch {
		case err != nil:
			failed = append(failed, fmt.Errorf("%s: %w", config.path, err))
		case changed:
			d.log.Printf("MCP relay: %s %s", map[bool]string{true: "removed from", false: "written to"}[config.off], config.path)
		}
	}
	if !settings.codexSetUp {
		return errors.Join(failed...)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	changed, err := codexsurface.ReconcileCodexRelay(ctx, codex, executable, settings.codexOff)
	switch {
	case err != nil:
		failed = append(failed, fmt.Errorf("the Codex config in %s: %w", codex.Codex, err))
	case changed:
		d.log.Printf("MCP relay: %s the Codex config in %s", map[bool]string{true: "removed from", false: "written to"}[settings.codexOff], codex.Codex)
	}
	return errors.Join(failed...)
}

type relayKeeper struct {
	home, executable string
	codex            codexsurface.CodexHome
	done             *relaySettings
}

func (k *relayKeeper) pass(ctx context.Context, d *Daemon) error {
	settings, err := readRelaySettings(k.home, k.codex)
	if err != nil || k.done != nil && settings.equal(*k.done) {
		return err
	}
	if err := d.reconcileMCPRelays(ctx, settings, k.codex, k.executable); err != nil {
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
