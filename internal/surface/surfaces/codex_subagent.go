package surfaces

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

type codexThreadSpawn struct {
	ParentThreadID string  `json:"parent_thread_id"`
	Depth          int     `json:"depth"`
	AgentNickname  *string `json:"agent_nickname"`
	AgentRole      *string `json:"agent_role"`
}

// The app-server Thread does not carry the family root; for nested subagents
// it comes from session_id in the rollout session_meta.
func codexSubagent(thread map[string]any) *surface.Subagent {
	spawn := codexThreadSpawnSource(thread["source"])
	parent := str(thread, "parentThreadId")
	if parent == "" && spawn != nil {
		parent = spawn.ParentThreadID
	}
	if parent == "" {
		return nil
	}
	subagent := &surface.Subagent{ParentID: parent, Depth: 1, Nickname: str(thread, "agentNickname"), Role: str(thread, "agentRole")}
	if spawn != nil {
		if spawn.Depth > 0 {
			subagent.Depth = spawn.Depth
		}
		if subagent.Nickname == "" && spawn.AgentNickname != nil {
			subagent.Nickname = *spawn.AgentNickname
		}
		if subagent.Role == "" && spawn.AgentRole != nil {
			subagent.Role = *spawn.AgentRole
		}
	}
	if subagent.Depth == 1 {
		subagent.RootID = parent
	} else if path := str(thread, "path"); path != "" {
		if meta, err := codexRolloutSubagents.read(path); err == nil && meta != nil && meta.ParentID == parent {
			subagent.RootID = meta.RootID
		}
	}
	return subagent
}

func codexThreadSpawnSource(value any) *codexThreadSpawn {
	source, _ := value.(map[string]any)
	sub, _ := source["subAgent"].(map[string]any)
	raw, found := sub["thread_spawn"]
	if !found {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var spawn codexThreadSpawn
	if json.Unmarshal(data, &spawn) != nil || spawn.ParentThreadID == "" {
		return nil
	}
	return &spawn
}

// A rollout's first line never changes, so each path is read once.
type codexRolloutSubagentCache struct {
	mu      sync.Mutex
	entries map[string]*surface.Subagent
}

var codexRolloutSubagents = &codexRolloutSubagentCache{entries: map[string]*surface.Subagent{}}

func (c *codexRolloutSubagentCache) read(path string) (*surface.Subagent, error) {
	c.mu.Lock()
	cached, found := c.entries[path]
	c.mu.Unlock()
	if found {
		return cached, nil
	}
	subagent, err := readCodexRolloutSubagent(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[path] = subagent
	c.mu.Unlock()
	return subagent, nil
}

type codexSessionMetaLine struct {
	Type    string `json:"type"`
	Payload struct {
		ID            string `json:"id"`
		SessionID     string `json:"session_id"`
		AgentNickname string `json:"agent_nickname"`
		AgentRole     string `json:"agent_role"`
		Source        any    `json:"source"`
	} `json:"payload"`
}

// The rollout spells the source key "subagent"; the app-server spells it
// "subAgent".
func readCodexRolloutSubagent(path string) (*surface.Subagent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	line, err := reader.ReadSlice('\n')
	if err != nil && err != bufio.ErrBufferFull && len(line) == 0 {
		return nil, fmt.Errorf("read Codex rollout %s: %w", path, err)
	}
	if err == bufio.ErrBufferFull {
		full := append([]byte(nil), line...)
		for err == bufio.ErrBufferFull {
			if len(full) > maxClaudeTranscriptRecordBytes {
				return nil, fmt.Errorf("Codex rollout %s session_meta exceeds %d bytes", path, maxClaudeTranscriptRecordBytes)
			}
			line, err = reader.ReadSlice('\n')
			full = append(full, line...)
		}
		line = full
	}
	var meta codexSessionMetaLine
	if err := json.Unmarshal(line, &meta); err != nil {
		return nil, fmt.Errorf("parse Codex rollout %s session_meta: %w", path, err)
	}
	if meta.Type != "session_meta" {
		return nil, fmt.Errorf("Codex rollout %s does not start with session_meta", path)
	}
	source, _ := meta.Payload.Source.(map[string]any)
	sub, _ := source["subagent"].(map[string]any)
	raw, found := sub["thread_spawn"]
	if !found {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var spawn codexThreadSpawn
	if err := json.Unmarshal(data, &spawn); err != nil || spawn.ParentThreadID == "" {
		return nil, fmt.Errorf("Codex rollout %s has an invalid thread_spawn source", path)
	}
	subagent := &surface.Subagent{ParentID: spawn.ParentThreadID, RootID: meta.Payload.SessionID, Depth: spawn.Depth, Nickname: meta.Payload.AgentNickname, Role: meta.Payload.AgentRole}
	if subagent.Nickname == "" && spawn.AgentNickname != nil {
		subagent.Nickname = *spawn.AgentNickname
	}
	if subagent.Role == "" && spawn.AgentRole != nil {
		subagent.Role = *spawn.AgentRole
	}
	if subagent.RootID == "" || subagent.RootID == meta.Payload.ID {
		return nil, fmt.Errorf("Codex rollout %s session_meta does not name its root session", path)
	}
	return subagent, nil
}
