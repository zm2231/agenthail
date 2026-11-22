package daemon

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func TestARootAddedWhileTheDaemonRunsGetsTheRelayAndAnEditedEntryIsLeftAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	previous := relaySettingsPoll
	relaySettingsPoll = 20 * time.Millisecond
	t.Cleanup(func() { relaySettingsPoll = previous })
	home := t.TempDir()
	primary, added := filepath.Join(home, ".claude"), filepath.Join(home, "work-claude")
	for _, dir := range []string{primary, added, filepath.Join(home, ".agenthail")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(home, "bin", "agenthail")
	edited := `{"mcpServers":{"agenthail":{"type":"stdio","command":"` + executable + `","args":["mcp","stdio","--verbose"],"env":{"AGENTHAIL_MCP_INSTALLED_BY":"agenthail mcp install"}}}}`
	primaryConfig := surface.ClaudeGlobalConfigPath(home, primary)
	if err := os.WriteFile(primaryConfig, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{log: log.New(io.Discard, "", 0), background: newBackgroundWork()}
	t.Cleanup(d.background.stop)
	d.keepMCPRelays(&relayKeeper{home: home, executable: executable, codex: codexsurface.CodexHome{Codex: filepath.Join(home, "no-codex")}})

	addedConfig := surface.ClaudeGlobalConfigPath(home, added)
	time.Sleep(5 * relaySettingsPoll)
	if data := readOrEmpty(addedConfig); len(data) != 0 {
		t.Fatalf("a root not yet configured got a config: %s", data)
	}
	config, _ := json.Marshal(map[string]any{"claudeConfigDirs": []string{added}})
	if err := os.WriteFile(surface.AgenthailConfigPath(home), config, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for gjson.GetBytes(readOrEmpty(addedConfig), "mcpServers.agenthail.command").String() != executable {
		if time.Now().After(deadline) {
			t.Fatalf("the added root never got the relay: %s", readOrEmpty(addedConfig))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := string(readOrEmpty(primaryConfig)); got != edited {
		t.Fatalf("the entry the person edited changed:\n%s", got)
	}
}

func readOrEmpty(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}
