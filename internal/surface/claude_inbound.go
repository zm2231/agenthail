package surface

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/tidwall/gjson"
)

type ClaudeInbound struct {
	Value string `json:"value,omitempty"`
	Path  string `json:"path,omitempty"`
}

func (i ClaudeInbound) Accepts() bool { return i.Value == "accept" }

// The first file that sets a value wins, in Claude Code's precedence order.
func ReadClaudeInbound(root, cwd string) ClaudeInbound {
	paths := []string{claudeManagedSettingsPath()}
	if cwd != "" {
		paths = append(paths, filepath.Join(cwd, ".claude", "settings.local.json"), filepath.Join(cwd, ".claude", "settings.json"))
	}
	paths = append(paths, ClaudeSettingsPath(root))
	for _, path := range paths {
		data, _, err := ReadClaudeSettingsFile(path)
		if err != nil {
			continue
		}
		if value := gjson.GetBytes(data, "crossSessionInbound"); value.Type == gjson.String {
			return ClaudeInbound{Value: value.String(), Path: path}
		}
	}
	return ClaudeInbound{}
}

func (i ClaudeInbound) HoldsLeadMessages(root string) string {
	if i.Accepts() {
		return ""
	}
	fix := fmt.Sprintf(`set "crossSessionInbound": "accept" in %s, or run the lead and its sessions in the same permission mode`, ClaudeSettingsPath(root))
	switch i.Value {
	case "":
		return "a lead running in a terminal does not receive a message from a session in another permission mode, including Agenthail's notices, until the person approves it there; to deliver them, " + fix
	case "refuse":
		return fmt.Sprintf("a lead running in a terminal receives no messages from other sessions, including Agenthail's notices (crossSessionInbound is refuse in %s); to deliver them, %s", i.Path, fix)
	}
	return fmt.Sprintf("a lead running in a terminal does not receive messages from other sessions, including Agenthail's notices, until the person approves each there (crossSessionInbound is %s in %s); to deliver them, %s", i.Value, i.Path, fix)
}

func claudeManagedSettingsPath() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	}
	return "/etc/claude-code/managed-settings.json"
}
