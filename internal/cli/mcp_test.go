package cli

import (
	"os"
	"path/filepath"
	"testing"
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
