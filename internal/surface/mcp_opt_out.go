package surface

import (
	"encoding/json"
	"fmt"
)

// Other entries keep their bytes, so the person's own way of writing them survives.
func SetClaudeRootMCP(home, root string, on bool) (bool, error) {
	config, err := ReadAgenthailConfig(home)
	if err != nil {
		return false, err
	}
	var entries []json.RawMessage
	if raw, ok := config["claudeConfigDirs"]; ok {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return false, fmt.Errorf("Agenthail config %s: claudeConfigDirs must be a list", AgenthailConfigPath(home))
		}
	}
	changed, found := false, false
	for i, raw := range entries {
		var entry claudeRootEntry
		if err := json.Unmarshal(raw, &entry); err != nil || !SameClaudeRoot(entry.Path, root) {
			continue
		}
		found = true
		off := entry.MCP != nil && !*entry.MCP
		if off == !on {
			continue
		}
		if entries[i], err = encodeClaudeRootEntry(entry, on); err != nil {
			return false, err
		}
		changed = true
	}
	if !found && !on {
		encoded, err := encodeClaudeRootEntry(claudeRootEntry{Path: root}, false)
		if err != nil {
			return false, err
		}
		entries, changed = append(entries, encoded), true
	}
	if !changed {
		return false, nil
	}
	if config["claudeConfigDirs"], err = json.Marshal(entries); err != nil {
		return false, err
	}
	return true, WriteAgenthailConfig(home, config)
}

func encodeClaudeRootEntry(entry claudeRootEntry, on bool) (json.RawMessage, error) {
	if on && entry.Proxy == "" {
		return json.Marshal(entry.Path)
	}
	entry.MCP = nil
	if !on {
		off := false
		entry.MCP = &off
	}
	return json.Marshal(entry)
}

func SetCodexMCP(home string, on bool) (bool, error) {
	config, err := ReadAgenthailConfig(home)
	if err != nil {
		return false, err
	}
	off, err := CodexMCPOff(home)
	if err != nil || off == !on {
		return false, err
	}
	if on {
		delete(config, "codexMCP")
	} else {
		config["codexMCP"] = json.RawMessage("false")
	}
	return true, WriteAgenthailConfig(home, config)
}
