package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const toolCardLine = `agenthail_send {"target":"<@alias|session-id>","message":"<message>"}`

func helpAgentAsCodex(t *testing.T, app *App, threadID string) string {
	t.Helper()
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	t.Setenv("CODEX_THREAD_ID", threadID)
	text, err := captureStdout(t, func() error { return app.Run([]string{"help", "agent"}) })
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func relayCommand(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agenthail")
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func setProjectTools(t *testing.T, app *App, value string) {
	t.Helper()
	if _, err := app.Registry.SetProjectSetting("project-card", registry.ProjectSettingMCP, value); err != nil {
		t.Fatal(err)
	}
}

// installRelayInRoot points CLAUDE_CONFIG_DIR at a root whose config starts
// the stdio relay.
func installRelayInRoot(t *testing.T) {
	t.Helper()
	home, _ := os.UserHomeDir()
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	entry := `{"mcpServers":{"agenthail":{"type":"stdio","command":"` + relayCommand(t) + `","args":["mcp","stdio"],"env":{"AGENTHAIL_MCP_INSTALLED_BY":"agenthail mcp install"}}}}`
	if err := os.WriteFile(surface.ClaudeGlobalConfigPath(home, root), []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestATerminalClaudeWhoseRootStartsTheRelayIsTaughtTheTools(t *testing.T) {
	app, _, _ := cardFixture(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if card := helpAgentAs(t, app, "planner-conversation"); strings.Contains(card, "agenthail_") {
		t.Fatalf("a terminal session without the server is taught tools:\n%s", card)
	}
	installRelayInRoot(t)
	if card := helpAgentAs(t, app, "planner-conversation"); !strings.Contains(card, toolCardLine) || strings.Contains(card, "agenthail thread create") {
		t.Fatalf("a terminal session whose root starts the relay is not taught the tools:\n%s", card)
	}
	setProjectTools(t, app, registry.ProjectMCPOff)
	if card := helpAgentAs(t, app, "planner-conversation"); strings.Contains(card, "agenthail_") {
		t.Fatalf("a session in a project with the tools off is taught them:\n%s", card)
	}
}

func TestAPersonsCodexThreadWhoseConfigStartsTheRelayIsTaughtTheTools(t *testing.T) {
	app, _, cwd := cardFixture(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := app.Registry.RegisterSession(surface.Session{ID: "persons-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
	command := relayCommand(t)
	relayWith := func(extra string) string {
		return "model = \"x\"\n\n[mcp_servers.agenthail_relay]\ncommand = \"" + command + "\"\nargs = [\"mcp\", \"stdio\"]\n" + extra + "\n[mcp_servers.agenthail_relay.env]\nAGENTHAIL_MCP_INSTALLED_BY = \"agenthail mcp install\"\n"
	}
	relay := relayWith("")
	config := filepath.Join(codexHome, "config.toml")

	if card := helpAgentAsCodex(t, app, "persons-thread"); strings.Contains(card, "agenthail_") {
		t.Fatalf("a Codex thread without the relay is taught tools:\n%s", card)
	}
	if err := os.WriteFile(config, []byte(relay), 0o600); err != nil {
		t.Fatal(err)
	}
	if card := helpAgentAsCodex(t, app, "persons-thread"); !strings.Contains(card, toolCardLine) || strings.Contains(card, "agenthail_end_turn") {
		t.Fatalf("a Codex thread whose config starts the relay is not taught the tools without ending its turns through one:\n%s", card)
	}
	if err := os.WriteFile(config, []byte(relayWith("enabled = false\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if card := helpAgentAsCodex(t, app, "persons-thread"); strings.Contains(card, "agenthail_") {
		t.Fatalf("a Codex thread whose relay is disabled is taught tools:\n%s", card)
	}
	if err := os.WriteFile(config, []byte(relay), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Registry.IssueSessionCredential("persons-thread", time.Now().Add(-registry.CredentialMaxLifetime-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if card := helpAgentAsCodex(t, app, "persons-thread"); strings.Contains(card, "agenthail_") {
		t.Fatalf("a thread Agenthail gave its own server, whose credential lapsed, is taught tools it has not got:\n%s", card)
	}
}
