package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestDaemonUninstallTakesAgenthailsMCPServerOutOfClaudesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(home, ".claude.json")
	others := `{"mcpServers":{"github":{"type":"http","url":"https://example.invalid/mcp"}}}`
	installed := `{"mcpServers":{"github":{"type":"http","url":"https://example.invalid/mcp"},"agenthail":{"type":"stdio","command":"/usr/local/bin/agenthail","args":["mcp","stdio"],"env":{"AGENTHAIL_MCP_INSTALLED_BY":"agenthail mcp install"}}}}`
	if err := os.WriteFile(config, []byte(installed), 0600); err != nil {
		t.Fatal(err)
	}
	app := &App{daemonService: &fakeService{}}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"daemon", "uninstall"}) }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(config); string(data) != others {
		t.Fatalf("after daemon uninstall .claude.json is\n%s", data)
	}
}

func TestARootWhoseConfigTurnsTheMCPServerOffLosesItOnInstallAndSaysSo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(surface.AgenthailConfigPath(home), []byte(`{"claudeConfigDirs":[{"path":"`+root+`","mcp":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude.json")
	installed := `{"mcpServers":{"agenthail":{"type":"stdio","command":"/usr/local/bin/agenthail","args":["mcp","stdio"],"env":{"AGENTHAIL_MCP_INSTALLED_BY":"agenthail mcp install"}}}}`
	if err := os.WriteFile(config, []byte(installed), 0600); err != nil {
		t.Fatal(err)
	}
	app := &App{daemonService: &fakeService{}}
	if status, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "status"}) }); err != nil || !strings.Contains(status, "off but installed; run: agenthail mcp install") {
		t.Fatalf("status = %q, %v", status, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"daemon", "install"}) }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(config); string(data) != `{"mcpServers":{}}` {
		t.Fatalf("daemon install left the server in a root that turns it off:\n%s", data)
	}
	if status, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "status"}) }); err != nil || !strings.Contains(status, ": agenthail MCP server off\n") {
		t.Fatalf("status = %q, %v", status, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "install"}) }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(config); string(data) != `{"mcpServers":{}}` {
		t.Fatalf("mcp install added the server to a root that turns it off:\n%s", data)
	}
}

func TestCodexMCPOffTakesTheRelayOutOfCodexsConfigOnInstallAndSaysSo(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	for _, dir := range []string{codexHome, filepath.Join(home, ".agenthail"), filepath.Join(home, ".claude")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(surface.AgenthailConfigPath(home), []byte(`{"codexMCP":false,"claudeConfigDirs":[{"path":"`+filepath.Join(home, ".claude")+`","mcp":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	persons := "# mine\nmodel = \"x\"\n"
	config := filepath.Join(codexHome, "config.toml")
	installed := persons + "\n[mcp_servers.agenthail_relay]\ncommand = \"/usr/local/bin/agenthail\"\nargs = [\"mcp\", \"stdio\"]\n\n[mcp_servers.agenthail_relay.env]\nAGENTHAIL_MCP_INSTALLED_BY = \"agenthail mcp install\"\n"
	if err := os.WriteFile(config, []byte(installed), 0600); err != nil {
		t.Fatal(err)
	}
	app := &App{daemonService: &fakeService{}}
	if status, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "status"}) }); err != nil || !strings.Contains(status, "agenthail_relay MCP server off but installed; run: agenthail mcp install") {
		t.Fatalf("status = %q, %v", status, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"daemon", "install"}) }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(config); string(data) != persons {
		t.Fatalf("daemon install left Codex's config\n%s\nwant\n%s", data, persons)
	}
	if status, err := captureStdout(t, func() error { return app.Run([]string{"mcp", "status"}) }); err != nil || !strings.Contains(status, "agenthail_relay MCP server off\n") {
		t.Fatalf("status = %q, %v", status, err)
	}
}
