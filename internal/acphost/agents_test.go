package acphost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestConfigEntriesAddReplaceAndRemoveAgents(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"acpAgents": [
		{"id": "devin", "label": "Devin (pinned)", "command": ["/opt/devin", "acp"]},
		{"id": "helper", "command": ["helper-agent", "--acp"], "modes": {"careful": "ask"}},
		{"id": "claude", "command": ["x"]},
		{"id": "loose", "command": ["x"], "modes": {"yolo": "everything"}},
		{"id": "gone", "disabled": true}
	]}`
	if err := os.WriteFile(surface.AgenthailConfigPath(home), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	agents, problems := LoadAgents(home)
	if len(agents) != 2 || agents[0].Command[0] != "/opt/devin" || agents[0].DisplayName() != "Devin (pinned)" || agents[1].ID != "helper" || agents[1].DisplayName() != "helper" {
		t.Fatalf("agents=%+v", agents)
	}
	if len(problems) != 2 {
		t.Fatalf("a reserved id and an unknown rank must each be reported: %v", problems)
	}
	if agents[1].ModeRank("careful") != "ask" || agents[1].ModeRank("unlisted") != "full" {
		t.Fatal("an unranked mode must count as full")
	}

	if err := os.WriteFile(surface.AgenthailConfigPath(home), []byte(`{"acpAgents": [{"id": "devin", "disabled": true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if agents, problems := LoadAgents(home); len(agents) != 0 || len(problems) != 0 {
		t.Fatalf("a disabled entry removes the built-in agent: %+v %v", agents, problems)
	}
}

func TestOnlyTheShippedAgentIsBuiltIn(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if agents, _ := LoadAgents(home); len(agents) != 1 || !agents[0].BuiltIn() {
		t.Fatalf("the shipped Devin is built in: %+v", agents)
	}
	shipped := BuiltInAgents()[0].Command
	override, _ := json.Marshal(map[string]any{"acpAgents": []map[string]any{{"id": "devin", "label": "My Devin", "command": shipped}, {"id": "helper", "command": []string{"helper-agent"}}}})
	if err := os.WriteFile(surface.AgenthailConfigPath(home), override, 0o600); err != nil {
		t.Fatal(err)
	}
	agents, problems := LoadAgents(home)
	if len(agents) != 2 || len(problems) != 0 {
		t.Fatalf("agents=%+v problems=%v", agents, problems)
	}
	for _, agent := range agents {
		if agent.BuiltIn() {
			t.Fatalf("an entry from the config is not built in, even with the shipped command: %+v", agent)
		}
	}
}
