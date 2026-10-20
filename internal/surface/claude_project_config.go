package surface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// Claude Code keeps per-folder state in the root's global config under
// projects[<cwd>]: folder trust, approvals and project-local MCP servers. A
// conversation moved to another root runs without them unless they are
// carried, and a project-local MCP server missing there takes its tools away.
var claudeProjectFlags = []string{"hasTrustDialogAccepted", "hasClaudeMdExternalIncludesApproved"}
var claudeProjectLists = []string{"allowedTools", "enabledMcpjsonServers", "disabledMcpjsonServers", "disabledMcpServers"}

// ClaudeMCPAdder registers a project-local MCP server through Claude Code
// itself, under root and in cwd.
type ClaudeMCPAdder func(ctx context.Context, root, cwd, name string, config json.RawMessage) error

// CarryClaudeProjectConfig gives cwd in root to the folder trust, approvals and
// project-local MCP servers it has in root from, adding to what to already
// holds and never removing from it. MCP servers go through Claude Code's own
// `mcp add-json`. The rest has no Claude command, so it is written the way
// Claude writes the file, to a temporary file renamed over it, and only when
// the file is unchanged since it was read; a concurrent write retries.
func CarryClaudeProjectConfig(ctx context.Context, home, from, to, cwd string, addMCP ClaudeMCPAdder) error {
	source, err := readClaudeProject(ClaudeGlobalConfigPath(home, from), cwd)
	if err != nil || source == nil {
		return err
	}
	target, err := readClaudeProject(ClaudeGlobalConfigPath(home, to), cwd)
	if err != nil {
		return err
	}
	var servers, existing orderedObject
	if raw, ok := source.get("mcpServers"); ok {
		_ = json.Unmarshal(raw, &servers)
	}
	if target != nil {
		if raw, ok := target.get("mcpServers"); ok {
			_ = json.Unmarshal(raw, &existing)
		}
	}
	for _, server := range servers {
		if _, ok := existing.get(server.Key); ok {
			continue
		}
		if err := addMCP(ctx, to, cwd, server.Key, server.Value); err != nil {
			return fmt.Errorf("carry MCP server %s to %s: %w", server.Key, to, err)
		}
	}
	return updateClaudeGlobalConfig(ClaudeGlobalConfigPath(home, to), func(config *orderedObject) bool {
		return mergeClaudeProject(config, cwd, source)
	})
}

// ClaudeCLIMCPAdder runs `claude mcp add-json --scope local` against root.
func ClaudeCLIMCPAdder(home string) ClaudeMCPAdder {
	return func(ctx context.Context, root, cwd, name string, config json.RawMessage) error {
		binary, err := ClaudeBinary(home)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, binary, "mcp", "add-json", "--scope", "local", name, string(config))
		cmd.Dir = cwd
		cmd.Env = append(AgentEnv(os.Environ()), "HOME="+home)
		if dir := ClaudeLaunchConfigDir(home, root); dir != "" {
			cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+dir)
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
		}
		return nil
	}
}

func readClaudeProject(path, cwd string) (*orderedObject, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config orderedObject
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	raw, ok := config.get("projects")
	if !ok {
		return nil, nil
	}
	var projects orderedObject
	if err := json.Unmarshal(raw, &projects); err != nil {
		return nil, fmt.Errorf("read %s projects: %w", path, err)
	}
	entry, ok := projects.get(cwd)
	if !ok {
		return nil, nil
	}
	var project orderedObject
	if err := json.Unmarshal(entry, &project); err != nil {
		return nil, fmt.Errorf("read %s project %s: %w", path, cwd, err)
	}
	return &project, nil
}

func mergeClaudeProject(config *orderedObject, cwd string, source *orderedObject) bool {
	var projects orderedObject
	if raw, ok := config.get("projects"); ok && json.Unmarshal(raw, &projects) != nil {
		return false
	}
	var project orderedObject
	if raw, ok := projects.get(cwd); ok && json.Unmarshal(raw, &project) != nil {
		return false
	}
	changed := false
	for _, key := range claudeProjectFlags {
		var from, to bool
		if raw, ok := source.get(key); ok {
			_ = json.Unmarshal(raw, &from)
		}
		if raw, ok := project.get(key); ok {
			_ = json.Unmarshal(raw, &to)
		}
		if from && !to {
			project.set(key, json.RawMessage("true"))
			changed = true
		}
	}
	for _, key := range claudeProjectLists {
		var from, to []string
		if raw, ok := source.get(key); ok {
			_ = json.Unmarshal(raw, &from)
		}
		if raw, ok := project.get(key); ok {
			_ = json.Unmarshal(raw, &to)
		}
		merged := slices.Clone(to)
		for _, value := range from {
			if !slices.Contains(merged, value) {
				merged = append(merged, value)
			}
		}
		if len(merged) == len(to) {
			continue
		}
		encoded, _ := json.Marshal(merged)
		project.set(key, encoded)
		changed = true
	}
	if !changed {
		return false
	}
	encodedProject, _ := json.Marshal(project)
	projects.set(cwd, encodedProject)
	encodedProjects, _ := json.Marshal(projects)
	config.set("projects", encodedProjects)
	return true
}

func updateClaudeGlobalConfig(path string, change func(*orderedObject) bool) error {
	for attempt := 0; attempt < 8; attempt++ {
		before, err := os.Stat(path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var config orderedObject
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if !change(&config) {
			return nil
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return err
		}
		var indented bytes.Buffer
		if err := json.Indent(&indented, encoded, "", "  "); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".agenthail-*")
		if err != nil {
			return err
		}
		_, writeErr := temp.Write(indented.Bytes())
		if writeErr == nil {
			writeErr = temp.Chmod(before.Mode().Perm())
		}
		if writeErr == nil {
			writeErr = temp.Sync()
		}
		if closeErr := temp.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = os.Remove(temp.Name())
			return writeErr
		}
		after, err := os.Stat(path)
		if err == nil && after.ModTime().Equal(before.ModTime()) && after.Size() == before.Size() {
			if err := os.Rename(temp.Name(), path); err != nil {
				_ = os.Remove(temp.Name())
				return err
			}
			return nil
		}
		_ = os.Remove(temp.Name())
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
	return fmt.Errorf("%s kept changing while Agenthail updated it; retry the move", path)
}

// orderedObject is a JSON object that keeps its keys in the order it was
// read, so a rewrite of Claude's config changes only what it means to.
type orderedObject []orderedMember

type orderedMember struct {
	Key   string
	Value json.RawMessage
}

func (o orderedObject) get(key string) (json.RawMessage, bool) {
	for _, member := range o {
		if member.Key == key {
			return member.Value, true
		}
	}
	return nil, false
}

func (o *orderedObject) set(key string, value json.RawMessage) {
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Value = value
			return
		}
	}
	*o = append(*o, orderedMember{Key: key, Value: value})
}

func (o *orderedObject) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("not a JSON object")
	}
	*o = (*o)[:0]
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		*o = append(*o, orderedMember{Key: key, Value: value})
	}
	_, err = decoder.Token()
	return err
}

func (o orderedObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for i, member := range o {
		if i > 0 {
			buffer.WriteByte(',')
		}
		key, err := json.Marshal(member.Key)
		if err != nil {
			return nil, err
		}
		buffer.Write(key)
		buffer.WriteByte(':')
		if len(member.Value) == 0 {
			buffer.WriteString("null")
		} else {
			buffer.Write(member.Value)
		}
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}
