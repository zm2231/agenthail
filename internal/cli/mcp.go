package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/claudemcp"
	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/mcpstdio"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

const mcpUsage = "usage: agenthail mcp stdio|install|uninstall|status"

var relayExecutable = surface.RelayExecutable

func (a *App) cmdMCP(args []string) error {
	if len(args) != 1 {
		return errors.New(mcpUsage)
	}
	switch args[0] {
	case "stdio":
		return mcpstdio.Run(context.Background(), daemonctl.MCPSocketPath(), a.Version, os.Stdin, os.Stdout)
	case "install", "uninstall", "status":
		return errors.Join(claudeMCPAt(args[0], true), codexMCPAt(args[0], true))
	}
	return errors.New(mcpUsage)
}

type claudeMCPConfig struct {
	root, path string
	off        bool
}

const (
	claudeMCPOff          = "off"
	claudeMCPOffInstalled = "off but installed"
)

func claudeMCPConfigs() ([]claudeMCPConfig, error) {
	home, _ := os.UserHomeDir()
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	configs := make([]claudeMCPConfig, len(roots))
	for i, root := range roots {
		off, err := surface.ClaudeRootMCPOff(home, root)
		if err != nil {
			return nil, err
		}
		configs[i] = claudeMCPConfig{root: root, path: surface.ClaudeGlobalConfigPath(home, root), off: off}
	}
	return configs, nil
}

// One root's broken config never keeps the others from their tools, and only the person's own install or uninstall changes the opt-out, so an upgrade keeps it.
func claudeMCPAt(action string, optOut bool) error {
	configs, err := claudeMCPConfigs()
	if err != nil {
		return err
	}
	executable := relayExecutable()
	home, _ := os.UserHomeDir()
	var failed []error
	for _, config := range configs {
		if executable == "" && (action == "install" && (optOut || !config.off) || action == "status" && !config.off) {
			failed = append(failed, fmt.Errorf("%s: run this from the installed agenthail binary; Claude runs the binary that wrote its MCP server", config.path))
			continue
		}
		var line string
		switch action {
		case "install":
			var cleared, changed bool
			switch {
			case config.off && !optOut:
				if changed, err = claudemcp.UninstallAt(config.path); err == nil {
					line = map[bool]string{true: "removed, since claudeConfigDirs turns it off for this root", false: "off for this root in claudeConfigDirs"}[changed]
				}
			default:
				if changed, err = claudemcp.InstallAt(config.path, executable); err == nil {
					line = map[bool]string{true: "installed", false: "already installed"}[changed]
					if optOut {
						cleared, err = surface.SetClaudeRootMCP(home, config.root, true)
					}
				}
			}
			if cleared {
				line += fmt.Sprintf(`; removed "mcp": false for %s from %s`, config.root, surface.AgenthailConfigPath(home))
			}
		case "uninstall":
			var changed, set bool
			if changed, err = claudemcp.UninstallAt(config.path); err == nil {
				line = map[bool]string{true: "removed", false: "not installed"}[changed]
				if optOut {
					set, err = surface.SetClaudeRootMCP(home, config.root, false)
				}
			}
			if set {
				line += fmt.Sprintf(`; set "mcp": false for %s in %s, so the daemon does not add it back; agenthail mcp install turns it back on`, config.root, surface.AgenthailConfigPath(home))
			}
		case "status":
			if line, err = claudeMCPState(config, executable); line == claudemcp.Outdated || line == claudemcp.Missing || line == claudemcp.Dangling {
				line += "; run: agenthail mcp install"
			} else if line == claudeMCPOffInstalled {
				line += "; run: agenthail mcp uninstall"
			}
		}
		if err != nil {
			failed = append(failed, err)
			continue
		}
		fmt.Printf("%s: agenthail MCP server %s\n", config.path, line)
	}
	return errors.Join(failed...)
}

func claudeMCPState(config claudeMCPConfig, executable string) (string, error) {
	state, err := claudemcp.StateAt(config.path, executable)
	if err != nil || !config.off {
		return state, err
	}
	if state == claudemcp.Missing || state == claudemcp.Taken {
		return claudeMCPOff, nil
	}
	return claudeMCPOffInstalled, nil
}

func codexMCPAt(action string, optOut bool) error {
	home := codexsurface.CodexHomeFromEnv()
	label := "codex " + filepath.Join(home.Codex, "config.toml")
	if !codexsurface.CodexSetUp(home) {
		if action == "status" {
			fmt.Printf("codex: Codex is not set up for this user\n")
		}
		return nil
	}
	userHome, _ := os.UserHomeDir()
	off, err := surface.CodexMCPOff(userHome)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	executable := relayExecutable()
	var line string
	switch action {
	case "install":
		var cleared, changed bool
		switch {
		case off && !optOut:
			if changed, err = codexsurface.UninstallCodexRelay(ctx, home); err == nil {
				line = map[bool]string{true: "removed, since codexMCP turns it off", false: "off in codexMCP"}[changed]
			}
		case executable == "":
			err = fmt.Errorf("%s: run this from the installed agenthail binary; Codex runs the binary that wrote its MCP server", label)
		default:
			if changed, err = codexsurface.InstallCodexRelay(ctx, home, executable); err == nil {
				line = map[bool]string{true: "installed", false: "already installed"}[changed]
				if optOut {
					cleared, err = surface.SetCodexMCP(userHome, true)
				}
			}
		}
		if cleared {
			line += fmt.Sprintf("; removed codexMCP false from %s", surface.AgenthailConfigPath(userHome))
		}
	case "uninstall":
		var changed, set bool
		if changed, err = codexsurface.UninstallCodexRelay(ctx, home); err == nil {
			line = map[bool]string{true: "removed", false: "not installed"}[changed]
			if optOut {
				set, err = surface.SetCodexMCP(userHome, false)
			}
		}
		if set {
			line += fmt.Sprintf("; set codexMCP false in %s, so the daemon does not add it back; agenthail mcp install turns it back on", surface.AgenthailConfigPath(userHome))
		}
	case "status":
		if line, err = codexMCPState(ctx, home, off, executable); line == codexsurface.CodexRelayOutdated || line == codexsurface.CodexRelayMissing || line == codexsurface.CodexRelayDangling {
			line += "; run: agenthail mcp install"
		} else if line == claudeMCPOffInstalled {
			line += "; run: agenthail mcp uninstall"
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s MCP server %s\n", label, codexsurface.CodexRelayServerName, line)
	return nil
}

func codexMCPState(ctx context.Context, home codexsurface.CodexHome, off bool, executable string) (string, error) {
	state, err := codexsurface.CodexRelayState(ctx, home, executable)
	if err != nil || !off || state == "" {
		return state, err
	}
	if state == codexsurface.CodexRelayMissing || state == codexsurface.CodexRelayTaken {
		return claudeMCPOff, nil
	}
	return claudeMCPOffInstalled, nil
}
