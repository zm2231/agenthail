package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/zm2231/agenthail/internal/surface"
)

// CodexRelayServerName is the stdio relay install puts in the person's Codex
// config. It is not named agenthail: Codex merges a thread's config into the
// user's, and the agenthail server Agenthail gives its own threads is a url.
const CodexRelayServerName = "agenthail_relay"

const (
	codexRelayKey         = "mcp_servers." + CodexRelayServerName
	codexRelayInstalledBy = "AGENTHAIL_MCP_INSTALLED_BY"
)

const (
	CodexRelayMissing   = "missing"
	CodexRelayOutdated  = "outdated"
	CodexRelayInstalled = "installed"
	CodexRelayTaken     = "taken"
	CodexRelayDangling  = "dangling"
)

func codexRelayEntry(executable string) map[string]any {
	return map[string]any{
		"command": executable, "args": []any{"mcp", "stdio"},
		"default_tools_approval_mode": "approve", "tool_timeout_sec": mcpToolTimeoutSec,
		"env": map[string]any{codexRelayInstalledBy: "agenthail mcp install"},
	}
}

// codexRelayDisabled is what a thread Agenthail starts carries for the relay:
// a whole stdio server, so it merges into the person's entry, or stands alone
// where there is none, as a server that never starts.
func codexRelayDisabled() map[string]any {
	return map[string]any{"command": "agenthail", "args": []any{"mcp", "stdio"}, "enabled": false}
}

func codexRelayServer(user map[string]any) map[string]any {
	servers, _ := user["mcp_servers"].(map[string]any)
	server, _ := servers[CodexRelayServerName].(map[string]any)
	return server
}

func ownsCodexRelay(server map[string]any) bool {
	env, _ := server["env"].(map[string]any)
	_, ok := env[codexRelayInstalledBy]
	return ok
}

func codexRelayState(user map[string]any, executable string) string {
	server := codexRelayServer(user)
	switch {
	case server == nil:
		return CodexRelayMissing
	case !ownsCodexRelay(server):
		return CodexRelayTaken
	}
	command, _ := server["command"].(string)
	if _, err := os.Stat(command); err != nil || !filepath.IsAbs(command) {
		return CodexRelayDangling
	}
	if codexJSON(server) != codexJSON(codexRelayEntry(executable)) {
		return CodexRelayOutdated
	}
	return CodexRelayInstalled
}

func CodexSetUp(home CodexHome) bool {
	_, err := os.Stat(home.Codex)
	return err == nil
}

func InstallCodexRelay(ctx context.Context, home CodexHome, executable string) (bool, error) {
	return editCodexRelay(ctx, home, func(user map[string]any) (any, bool, error) {
		switch codexRelayState(user, executable) {
		case CodexRelayInstalled:
			return nil, false, nil
		case CodexRelayTaken:
			return nil, false, fmt.Errorf("the Codex config already has an MCP server named %s that Agenthail did not add; rename or remove it, then install again", CodexRelayServerName)
		}
		return codexRelayEntry(executable), true, nil
	})
}

func UninstallCodexRelay(ctx context.Context, home CodexHome) (bool, error) {
	if !CodexSetUp(home) {
		return false, nil
	}
	return editCodexRelay(ctx, home, func(user map[string]any) (any, bool, error) {
		server := codexRelayServer(user)
		return nil, server != nil && ownsCodexRelay(server), nil
	})
}

func editCodexRelay(ctx context.Context, home CodexHome, edit func(user map[string]any) (value any, write bool, err error)) (bool, error) {
	client, err := openCodexConfig(ctx, home)
	if err != nil {
		return false, err
	}
	defer client.Close()
	return writeCodexUserConfig(ctx, client, func(user map[string]any) ([]map[string]any, error) {
		value, write, err := edit(user)
		if err != nil || !write {
			return nil, err
		}
		return []map[string]any{{"keyPath": codexRelayKey, "mergeStrategy": "replace", "value": value}}, nil
	})
}

func CodexRelayState(ctx context.Context, home CodexHome, executable string) (string, error) {
	if !CodexSetUp(home) {
		return "", nil
	}
	client, err := openCodexConfig(ctx, home)
	if err != nil {
		return "", err
	}
	defer client.Close()
	user, _, err := readCodexUserConfig(ctx, client)
	if err != nil {
		return "", err
	}
	return codexRelayState(user, executable), nil
}

// HoldsThread compares with symlinks resolved, since Codex may report a
// rollout under the resolved form of a CODEX_HOME that passes through a link.
func (h CodexHome) HoldsThread(session surface.Session) bool {
	path := h.transcriptPath(&session)
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(resolvedCodexPath(h.Codex), resolvedCodexPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CodexRelayConfigured reads config.toml itself rather than through Codex,
// since a hook that runs at every prompt cannot start an app-server.
func CodexRelayConfigured(home CodexHome) bool {
	var user map[string]any
	if _, err := toml.DecodeFile(filepath.Join(home.Codex, "config.toml"), &user); err != nil {
		return false
	}
	server := codexRelayServer(user)
	if enabled, set := server["enabled"].(bool); set && !enabled {
		return false
	}
	state := codexRelayState(user, "")
	return state == CodexRelayInstalled || state == CodexRelayOutdated
}
