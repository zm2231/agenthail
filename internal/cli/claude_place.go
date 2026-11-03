package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

const rootsDefaultUsage = "usage: agenthail roots default [--json] | roots default <root>|none"

func (a *App) placeClaudeSession(creator string) (claudepick.Pick, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return claudepick.Pick{}, err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return claudepick.Pick{}, err
	}
	return claudepick.Place(roots, claudepick.Origin{CreatorTranscript: a.claudeTranscript(creator)}, func() (string, error) {
		config, err := daemon.LoadDashboardConfig()
		if err != nil {
			return "", err
		}
		return config.ClaudeDefaultRoot(), nil
	})
}

func (a *App) claudeTranscript(id string) string {
	if id == "" || a.registry() == nil {
		return ""
	}
	session, err := a.registry().Session(id)
	if err != nil || session.Surface != surface.KindClaude {
		return ""
	}
	return session.Transcript
}

func (a *App) cmdRootsDefault(args []string) error {
	positional := stripFlags(args)[1:]
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return err
	}
	switch len(positional) {
	case 0:
		pick, err := a.placeClaudeSession("")
		if err != nil {
			return err
		}
		if hasFlag(args, "--json") {
			return emit(map[string]any{"defaultRoot": config.ClaudeDefaultRoot(), "pick": pick})
		}
		chosen := config.ClaudeDefaultRoot()
		if chosen == "" {
			chosen = "none"
		}
		fmt.Printf("default root for new sessions: %s\na session you start goes to: %s (%s)\nan agent's session goes to the agent's root\n", chosen, pickTitle(pick), pick.Reason)
		return nil
	case 1:
		name := positional[0]
		if name == "none" {
			name = ""
		} else if err := knownRoot(name); err != nil {
			return err
		}
		config.SetClaudeDefaultRoot(name)
	default:
		return errors.New(rootsDefaultUsage)
	}
	if err := daemon.SaveDashboardConfig(config); err != nil {
		return err
	}
	fmt.Println("saved; new Claude sessions on this host follow it")
	return nil
}

func knownRoot(name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(roots))
	for _, root := range roots {
		names = append(names, root.Name)
	}
	if !slices.Contains(names, name) {
		return fmt.Errorf("no configured Claude config root is named %q; choose one of %v", name, names)
	}
	return nil
}

func pickTitle(pick claudepick.Pick) string {
	if pick.Account != "" {
		return pick.Name + ", " + pick.Account
	}
	return pick.Name
}
