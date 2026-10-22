package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/zm2231/agenthail/internal/agentcard"
)

const (
	CodexHooksMissing   = "missing"
	CodexHooksOutdated  = "outdated"
	CodexHooksInstalled = "installed"
)

const (
	codexMemoryBegin = "<!-- agenthail:begin -->"
	codexMemoryEnd   = "<!-- agenthail:end -->"
)

func codexMemoryBlock() string {
	return codexMemoryBegin + "\n" + agentcard.Guide + " Run `agenthail help agent` for who you are now and the commands that act on it.\n" + codexMemoryEnd + "\n"
}

type codexListedHook struct {
	Key         string `json:"key"`
	EventName   string `json:"eventName"`
	Command     string `json:"command"`
	TimeoutSec  int    `json:"timeoutSec"`
	TrustStatus string `json:"trustStatus"`
	CurrentHash string `json:"currentHash"`
}

// ownsCodexContextHook reports whether a Codex hook command is the
// session-context hook as Agenthail writes it, for any agenthail binary.
func ownsCodexContextHook(command string) bool {
	quoted, found := strings.CutSuffix(strings.TrimPrefix(command, "exec "), " hook session-context --surface codex")
	if !found || len(quoted) < 2 || quoted[0] != '\'' || quoted[len(quoted)-1] != '\'' {
		return false
	}
	path := strings.ReplaceAll(quoted[1:len(quoted)-1], `'\''`, "'")
	return filepath.IsAbs(path) && filepath.Base(path) == "agenthail"
}

func codexBinary() (string, error) {
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}
	if daemon := DetectCodexInstallation().Daemon; daemon != "" {
		return daemon, nil
	}
	return "", errors.New("codex is not installed")
}

func openCodexConfig(ctx context.Context) (*codexStdioClient, error) {
	binary, err := codexBinary()
	if err != nil {
		return nil, err
	}
	return startCodexStdio(ctx, binary, os.Environ())
}

func listCodexHooks(ctx context.Context, client *codexStdioClient) ([]codexListedHook, error) {
	response, err := client.Request(ctx, "hooks/list", map[string]any{"cwds": []string{codexHome()}}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(response["result"])
	var result struct {
		Data []struct {
			Hooks []codexListedHook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	var owned []codexListedHook
	for _, entry := range result.Data {
		for _, hook := range entry.Hooks {
			if ownsCodexContextHook(hook.Command) {
				owned = append(owned, hook)
			}
		}
	}
	return owned, nil
}

// InstallCodexContext has the person's own Codex sessions told who they are:
// the session-context hooks in the hooks file the person already uses
// (hooks.json when CODEX_HOME has one, else config.toml, written by Codex),
// trusted with the hash Codex reports for them, and Agenthail's standing
// instructions in a marked block of CODEX_HOME/AGENTS.md.
func InstallCodexContext(ctx context.Context, executable string) (bool, error) {
	command := CodexContextHookCommand(executable)
	changed, err := writeCodexMemory(true)
	if err != nil {
		return false, err
	}
	client, err := openCodexConfig(ctx)
	if err != nil {
		return false, err
	}
	defer client.Close()
	edited, err := editCodexHooks(ctx, client, func(groups []any) []any { return withCodexContextHook(groups, command) })
	if err != nil {
		return false, err
	}
	changed = changed || edited
	owned, err := listCodexHooks(ctx, client)
	if err != nil {
		return false, err
	}
	var trust []map[string]any
	for _, hook := range owned {
		if hook.TrustStatus != "trusted" {
			trust = append(trust, map[string]any{"keyPath": codexHookStateKey(hook.Key) + ".trusted_hash", "mergeStrategy": "replace", "value": hook.CurrentHash})
		}
	}
	if len(trust) > 0 {
		if _, err := client.Request(ctx, "config/batchWrite", map[string]any{"edits": trust}, 30*time.Second); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func UninstallCodexContext(ctx context.Context) (bool, error) {
	changed, err := writeCodexMemory(false)
	if err != nil {
		return false, err
	}
	client, err := openCodexConfig(ctx)
	if err != nil {
		return false, err
	}
	defer client.Close()
	owned, err := listCodexHooks(ctx, client)
	if err != nil {
		return false, err
	}
	edited, err := editCodexHooks(ctx, client, withoutCodexContextHook)
	if err != nil {
		return false, err
	}
	changed = changed || edited
	var untrust []map[string]any
	for _, hook := range owned {
		untrust = append(untrust, map[string]any{"keyPath": codexHookStateKey(hook.Key), "mergeStrategy": "replace", "value": nil})
	}
	if len(untrust) > 0 {
		if _, err := client.Request(ctx, "config/batchWrite", map[string]any{"edits": untrust}, 30*time.Second); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// CodexContextState is installed when both hooks run this binary trusted
// and AGENTS.md has the current block, missing when none of it is there, and
// empty when this user has no Codex.
func CodexContextState(ctx context.Context, executable string) (string, error) {
	if _, err := os.Stat(codexHome()); os.IsNotExist(err) {
		return "", nil
	}
	memory, err := os.ReadFile(filepath.Join(codexHome(), "AGENTS.md"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	client, err := openCodexConfig(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()
	owned, err := listCodexHooks(ctx, client)
	if err != nil {
		return "", err
	}
	hasBlock := strings.Contains(string(memory), codexMemoryBegin)
	if len(owned) == 0 && !hasBlock {
		return CodexHooksMissing, nil
	}
	current := map[string]bool{}
	command := CodexContextHookCommand(executable)
	for _, hook := range owned {
		if hook.Command == command && hook.TrustStatus == "trusted" && hook.TimeoutSec == codexContextHookTimeout {
			current[hook.EventName] = true
		}
	}
	if current["sessionStart"] && current["userPromptSubmit"] && len(owned) == len(codexContextEvents) && strings.Contains(string(memory), codexMemoryBlock()) {
		return CodexHooksInstalled, nil
	}
	return CodexHooksOutdated, nil
}

func codexHookStateKey(key string) string {
	return `hooks.state."` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key) + `"`
}

func withCodexContextHook(groups []any, command string) []any {
	return append(withoutCodexContextHook(groups), map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": codexContextHookTimeout}}})
}

// withoutCodexContextHook drops Agenthail's handlers, and a group they leave
// empty, and keeps every other hook where it is.
func withoutCodexContextHook(groups []any) []any {
	kept := []any{}
	for _, group := range groups {
		entry, _ := group.(map[string]any)
		handlers, _ := entry["hooks"].([]any)
		var others []any
		for _, handler := range handlers {
			hook, _ := handler.(map[string]any)
			text, ok := hook["command"].(string)
			if !ok || ownsCodexContextHook(text) {
				continue
			}
			others = append(others, handler)
		}
		if len(others) == 0 && len(handlers) > 0 {
			continue
		}
		entry["hooks"] = others
		kept = append(kept, entry)
	}
	return kept
}

// editCodexHooks applies edit to each event's matcher groups in the hooks
// file the person uses: hooks.json as a file Agenthail rewrites by path,
// config.toml through Codex, which keeps the rest of it as written.
func editCodexHooks(ctx context.Context, client *codexStdioClient, edit func([]any) []any) (bool, error) {
	path := filepath.Join(codexHome(), "hooks.json")
	if data, err := os.ReadFile(path); err == nil {
		updated := data
		for _, e := range codexContextEvents {
			var groups []any
			if raw := gjson.GetBytes(data, "hooks."+e.event); raw.Exists() {
				if err := json.Unmarshal([]byte(raw.Raw), &groups); err != nil {
					return false, fmt.Errorf("read %s: %w", path, err)
				}
			}
			next := edit(groups)
			if equalJSON(groups, next) {
				continue
			}
			if len(next) == 0 {
				updated, err = sjson.DeleteBytes(updated, "hooks."+e.event)
			} else {
				updated, err = sjson.SetBytes(updated, "hooks."+e.event, next)
			}
			if err != nil {
				return false, err
			}
		}
		if string(updated) == string(data) {
			return false, nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return false, err
		}
		return true, os.WriteFile(path, updated, info.Mode().Perm())
	} else if !os.IsNotExist(err) {
		return false, err
	}
	response, err := client.Request(ctx, "config/read", map[string]any{"includeLayers": true}, 30*time.Second)
	if err != nil {
		return false, err
	}
	var user map[string]any
	version := ""
	for _, layer := range gjson.Get(codexJSON(response), "result.layers").Array() {
		if layer.Get("name.type").String() == "user" {
			_ = json.Unmarshal([]byte(layer.Get("config").Raw), &user)
			version = layer.Get("version").String()
		}
	}
	hooks, _ := user["hooks"].(map[string]any)
	var edits []map[string]any
	for _, e := range codexContextEvents {
		groups, _ := hooks[e.event].([]any)
		next := edit(cloneJSON(groups))
		if equalJSON(groups, next) {
			continue
		}
		var value any = next
		if len(next) == 0 {
			value = nil
		}
		edits = append(edits, map[string]any{"keyPath": "hooks." + e.event, "mergeStrategy": "replace", "value": value})
	}
	if len(edits) == 0 {
		return false, nil
	}
	params := map[string]any{"edits": edits}
	if version != "" {
		params["expectedVersion"] = version
	}
	if _, err := client.Request(ctx, "config/batchWrite", params, 30*time.Second); err != nil {
		return false, err
	}
	return true, nil
}

func writeCodexMemory(install bool) (bool, error) {
	path := filepath.Join(codexHome(), "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	text := string(data)
	without := text
	if start := strings.Index(text, codexMemoryBegin); start >= 0 {
		end := strings.Index(text[start:], codexMemoryEnd)
		if end < 0 {
			return false, fmt.Errorf("%s has %s without %s; fix it by hand", path, codexMemoryBegin, codexMemoryEnd)
		}
		end += start + len(codexMemoryEnd)
		if end < len(text) && text[end] == '\n' {
			end++
		}
		without = text[:start] + text[end:]
	}
	updated := strings.TrimRight(without, "\n")
	if install {
		if updated != "" {
			updated += "\n\n"
		}
		updated += codexMemoryBlock()
	} else if updated != "" {
		updated += "\n"
	}
	if updated == text {
		return false, nil
	}
	if updated == "" {
		return true, os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(updated), 0600)
}

func codexJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func cloneJSON(groups []any) []any {
	var out []any
	_ = json.Unmarshal([]byte(codexJSON(groups)), &out)
	return out
}

func equalJSON(a, b []any) bool {
	return codexJSON(a) == codexJSON(b)
}
