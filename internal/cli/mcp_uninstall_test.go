package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/mcprelay"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func TestMCPUninstallKeepsTheRelayOutThroughTheDaemonsReconcileAndAnUpgrade(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", filepath.Join(home, "no-codex"))
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude.json")
	installed := `{"mcpServers":{"agenthail":{"type":"stdio","command":"/usr/local/bin/agenthail","args":["mcp","stdio"],"env":{"AGENTHAIL_MCP_INSTALLED_BY":"agenthail mcp install"}}}}`
	if err := os.WriteFile(config, []byte(installed), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{daemonService: &fakeService{}}
	out, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "uninstall"}) })
	if err != nil || !strings.Contains(out, `set "mcp": false for `+root) || !strings.Contains(out, "agenthail mcp install turns it back on") {
		t.Fatalf("uninstall printed %q, %v", out, err)
	}
	if off, err := surface.ClaudeRootMCPOff(home, root); err != nil || !off {
		t.Fatalf("after uninstall the root is off=%v, %v", off, err)
	}

	codex := codexsurface.CodexHomeFromEnv()
	settings, err := mcprelay.Read(home, codex)
	if err != nil {
		t.Fatal(err)
	}
	if changes, err := mcprelay.Reconcile(context.Background(), settings, codex, "/opt/agenthail/bin/agenthail"); err != nil || len(changes) != 0 {
		t.Fatalf("the daemon's reconcile after uninstall changed %v, %v", changes, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"daemon", "install"}) }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(config); gjson.GetBytes(data, "mcpServers.agenthail").Exists() {
		t.Fatalf("the relay came back after uninstall:\n%s", data)
	}
	if off, err := surface.ClaudeRootMCPOff(home, root); err != nil || !off {
		t.Fatalf("daemon install cleared the person's opt-out: off=%v, %v", off, err)
	}
	if cleared, err := surface.SetClaudeRootMCP(home, root, true); err != nil || !cleared {
		t.Fatalf("turning it back on = %v, %v", cleared, err)
	}
	if data, _ := os.ReadFile(surface.AgenthailConfigPath(home)); !strings.Contains(string(data), `"`+root+`"`) || strings.Contains(string(data), `"mcp"`) {
		t.Fatalf("after turning it back on the config is\n%s", data)
	}
}

func TestAnMCPInstallThatFindsThePersonsOwnServerKeepsTheirOptOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	root := filepath.Join(home, ".claude")
	for _, dir := range []string{root, codexHome, filepath.Join(home, ".agenthail")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	relay := relayCommand(t)
	relayExecutable = func() string { return relay }
	t.Cleanup(func() { relayExecutable = surface.RelayExecutable })
	if err := os.WriteFile(surface.AgenthailConfigPath(home), []byte(`{"codexMCP":false,"claudeConfigDirs":[{"path":"`+root+`","mcp":false}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	persons := `{"mcpServers":{"agenthail":{"type":"stdio","command":"/opt/mine/agenthail-proxy"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(persons), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("[mcp_servers.agenthail_relay]\ncommand = \"/opt/mine/agenthail-proxy\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{daemonService: &fakeService{}}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "install"}) }); err == nil {
		t.Fatal("mcp install over the person's own server succeeded")
	}
	if off, err := surface.ClaudeRootMCPOff(home, root); err != nil || !off {
		t.Fatalf("a failed install cleared the root's opt-out: off=%v, %v", off, err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".claude.json")); string(data) != persons {
		t.Fatalf("a failed install rewrote the person's server:\n%s", data)
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	if off, err := surface.CodexMCPOff(home); err != nil || !off {
		t.Fatalf("a failed install cleared codexMCP: off=%v, %v", off, err)
	}
}
