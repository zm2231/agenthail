package surface

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

const DefaultIdleCompactionMinTokens = 200_000

// IdleCompaction is Agenthail's own compaction of an idle Claude session under
// a config root, kept in the Agenthail config because Claude's own
// idleCompaction skips whenever the account is near its limit.
type IdleCompaction struct {
	Enabled          bool  `json:"enabled"`
	MinContextTokens int64 `json:"minContextTokens"`
}

type idleCompactionEntry struct {
	Enabled          *bool  `json:"enabled,omitempty"`
	MinContextTokens *int64 `json:"minContextTokens,omitempty"`
}

const idleCompactionKey = "claudeIdleCompaction"

func ReadIdleCompaction(home, root string) (IdleCompaction, error) {
	entries, err := idleCompactionEntries(home)
	if err != nil {
		return IdleCompaction{}, err
	}
	settings := IdleCompaction{Enabled: true, MinContextTokens: DefaultIdleCompactionMinTokens}
	if key, ok := idleCompactionEntryKey(entries, root); ok {
		entry := entries[key]
		if entry.Enabled != nil {
			settings.Enabled = *entry.Enabled
		}
		if entry.MinContextTokens != nil {
			settings.MinContextTokens = *entry.MinContextTokens
		}
	}
	return settings, nil
}

func writeIdleCompaction(home, root string, enabled *bool, minTokens *int64) error {
	if enabled == nil && minTokens == nil {
		return nil
	}
	if err := validIdleCompactionMinTokens(minTokens); err != nil {
		return err
	}
	config, err := ReadAgenthailConfig(home)
	if err != nil {
		return err
	}
	entries, err := parseIdleCompaction(home, config)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = map[string]idleCompactionEntry{}
	}
	key, ok := idleCompactionEntryKey(entries, root)
	if !ok {
		key = filepath.Clean(root)
	}
	entry := entries[key]
	if enabled != nil {
		entry.Enabled = enabled
	}
	if minTokens != nil {
		entry.MinContextTokens = minTokens
	}
	entries[key] = entry
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	config[idleCompactionKey] = raw
	return WriteAgenthailConfig(home, config)
}

func validIdleCompactionMinTokens(minTokens *int64) error {
	if minTokens != nil && (*minTokens < ClaudeAutoCompactMinTokens || *minTokens > ClaudeAutoCompactMaxTokens) {
		return fmt.Errorf("the idle compaction threshold must be a token count from %d to %d; got %d", ClaudeAutoCompactMinTokens, ClaudeAutoCompactMaxTokens, *minTokens)
	}
	return nil
}

func idleCompactionEntries(home string) (map[string]idleCompactionEntry, error) {
	config, err := ReadAgenthailConfig(home)
	if err != nil {
		return nil, err
	}
	return parseIdleCompaction(home, config)
}

func parseIdleCompaction(home string, config map[string]json.RawMessage) (map[string]idleCompactionEntry, error) {
	raw, ok := config[idleCompactionKey]
	if !ok {
		return nil, nil
	}
	var entries map[string]idleCompactionEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("Agenthail config %s: %s must map each Claude config root to {\"enabled\", \"minContextTokens\"}", AgenthailConfigPath(home), idleCompactionKey)
	}
	return entries, nil
}

func idleCompactionEntryKey(entries map[string]idleCompactionEntry, root string) (string, bool) {
	identity := claudeRootIdentity(filepath.Clean(root))
	for key := range entries {
		if claudeRootIdentity(filepath.Clean(key)) == identity {
			return key, true
		}
	}
	return "", false
}
