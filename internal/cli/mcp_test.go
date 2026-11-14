package cli

import (
	"os"
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
