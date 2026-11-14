package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/claudemcp"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/mcpstdio"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const mcpUsage = "usage: agenthail mcp stdio|install|uninstall|status"

func (a *App) cmdMCP(args []string) error {
	if len(args) != 1 {
		return errors.New(mcpUsage)
	}
	switch args[0] {
	case "stdio":
		return mcpstdio.Run(context.Background(), daemon.MCPSocketPath(), a.Version, os.Stdin, os.Stdout)
	case "install", "uninstall", "status":
		return errors.Join(claudeMCPAt(args[0]), codexMCPAt(args[0]))
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

// claudeMCPAt acts on every root, so one root's broken config does not keep
// the others from their tools.
func claudeMCPAt(action string) error {
	configs, err := claudeMCPConfigs()
	if err != nil {
		return err
	}
	executable := relayExecutable()
	var failed []error
	for _, config := range configs {
		if executable == "" && action != "uninstall" && !config.off {
			failed = append(failed, fmt.Errorf("%s: run this from the installed agenthail binary; Claude runs the binary that wrote its MCP server", config.path))
			continue
		}
		var line string
		switch action {
		case "install":
			var changed bool
			if config.off {
				if changed, err = claudemcp.UninstallAt(config.path); err == nil {
					line = map[bool]string{true: "removed, since claudeConfigDirs turns it off for this root", false: "off for this root in claudeConfigDirs"}[changed]
				}
			} else if changed, err = claudemcp.InstallAt(config.path, executable); err == nil {
				line = map[bool]string{true: "installed", false: "already installed"}[changed]
			}
		case "uninstall":
			var changed bool
			if changed, err = claudemcp.UninstallAt(config.path); err == nil {
				line = map[bool]string{true: "removed", false: "not installed"}[changed]
			}
		case "status":
			if line, err = claudeMCPState(config, executable); line == claudemcp.Outdated || line == claudemcp.Missing || line == claudemcp.Dangling || line == claudeMCPOffInstalled {
				line += "; run: agenthail mcp install"
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

// Homebrew deletes a version's Cellar directory on upgrade; its opt link
// follows the installed version.
func relayExecutable() string {
	executable := surfaces.AgenthailExecutable()
	marker := strings.Index(executable, "/Cellar/agenthail/")
	if marker <= 0 {
		return executable
	}
	linked := filepath.Join(executable[:marker], "opt", "agenthail", "bin", "agenthail")
	if _, err := os.Stat(linked); err == nil {
		return linked
	}
	return executable
}

func codexMCPAt(action string) error {
	home := surfaces.CodexHomeFromEnv()
	label := "codex " + filepath.Join(home.Codex, "config.toml")
	if !surfaces.CodexSetUp(home) {
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
		var changed bool
		if off {
			if changed, err = surfaces.UninstallCodexRelay(ctx, home); err == nil {
				line = map[bool]string{true: "removed, since codexMCP turns it off", false: "off in codexMCP"}[changed]
			}
		} else if executable == "" {
			err = fmt.Errorf("%s: run this from the installed agenthail binary; Codex runs the binary that wrote its MCP server", label)
		} else if changed, err = surfaces.InstallCodexRelay(ctx, home, executable); err == nil {
			line = map[bool]string{true: "installed", false: "already installed"}[changed]
		}
	case "uninstall":
		var changed bool
		if changed, err = surfaces.UninstallCodexRelay(ctx, home); err == nil {
			line = map[bool]string{true: "removed", false: "not installed"}[changed]
		}
	case "status":
		if line, err = codexMCPState(ctx, home, off, executable); line == surfaces.CodexRelayOutdated || line == surfaces.CodexRelayMissing || line == surfaces.CodexRelayDangling || line == claudeMCPOffInstalled {
			line += "; run: agenthail mcp install"
		}
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s MCP server %s\n", label, surfaces.CodexRelayServerName, line)
	return nil
}

func codexMCPState(ctx context.Context, home surfaces.CodexHome, off bool, executable string) (string, error) {
	state, err := surfaces.CodexRelayState(ctx, home, executable)
	if err != nil || !off || state == "" {
		return state, err
	}
	if state == surfaces.CodexRelayMissing || state == surfaces.CodexRelayTaken {
		return claudeMCPOff, nil
	}
	return claudeMCPOffInstalled, nil
}
