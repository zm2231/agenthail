// Package mcprelay keeps the stdio relay in every Claude root and in Codex as install would, without touching an entry the person edited.
package mcprelay

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

type claudeConfig struct {
	path string
	off  bool
}

type Settings struct {
	claude     []claudeConfig
	codexSetUp bool
	codexOff   bool
}

func (s Settings) Equal(other Settings) bool {
	return slices.Equal(s.claude, other.claude) && s.codexSetUp == other.codexSetUp && s.codexOff == other.codexOff
}

func Read(home string, codex codexsurface.CodexHome) (Settings, error) {
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	for _, root := range roots {
		// A root Claude has not created yet would start signed out; its config is not ours to create.
		if _, err := os.Stat(root); err != nil {
			continue
		}
		off, err := surface.ClaudeRootMCPOff(home, root)
		if err != nil {
			return Settings{}, err
		}
		settings.claude = append(settings.claude, claudeConfig{path: surface.ClaudeGlobalConfigPath(home, root), off: off})
	}
	settings.codexSetUp = codexsurface.CodexSetUp(codex)
	if settings.codexOff, err = surface.CodexMCPOff(home); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// Reconcile returns a line for each config it changed.
func Reconcile(ctx context.Context, settings Settings, codex codexsurface.CodexHome, executable string) ([]string, error) {
	var changes []string
	var failed []error
	verb := map[bool]string{true: "removed from", false: "written to"}
	for _, config := range settings.claude {
		changed, err := claudemcp.ReconcileAt(config.path, executable, config.off)
		switch {
		case err != nil:
			failed = append(failed, fmt.Errorf("%s: %w", config.path, err))
		case changed:
			changes = append(changes, verb[config.off]+" "+config.path)
		}
	}
	if !settings.codexSetUp {
		return changes, errors.Join(failed...)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	changed, err := codexsurface.ReconcileCodexRelay(ctx, codex, executable, settings.codexOff)
	switch {
	case err != nil:
		failed = append(failed, fmt.Errorf("the Codex config in %s: %w", codex.Codex, err))
	case changed:
		changes = append(changes, verb[settings.codexOff]+" the Codex config in "+codex.Codex)
	}
	return changes, errors.Join(failed...)
}
