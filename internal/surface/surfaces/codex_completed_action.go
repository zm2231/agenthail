package surfaces

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

type codexAction struct {
	Tool     string
	Input    map[string]any
	Output   string
	Failed   bool
	Command  string
	ExitCode *int
	Duration time.Duration
}

var codexMirroredItems = []string{"UserMessage", "AgentMessage", "Reasoning", "ContextCompaction"}

// Mirrored items repeat records that already render as messages or notices.
func codexCompletedAction(item map[string]any) (action codexAction, ok bool, mirrored bool) {
	typ := str(item, "type")
	if slices.Contains(codexMirroredItems, typ) {
		return codexAction{}, false, true
	}
	failed := str(item, "status") == "failed"
	switch typ {
	case "CommandExecution":
		command := codexExecutedCommand(item)
		action = codexAction{Tool: "exec_command", Input: map[string]any{"cmd": command}, Output: codexFirstString(item, "aggregated_output", "formatted_output", "stdout"), Failed: failed, Command: command, Duration: codexItemDuration(item)}
		if cwd := strings.TrimPrefix(str(item, "cwd"), "file://"); cwd != "" {
			action.Input["workdir"] = cwd
		}
		if code, ok := item["exit_code"].(float64); ok {
			exit := int(code)
			action.ExitCode = &exit
			action.Failed = action.Failed || exit != 0
		}
	case "McpToolCall":
		action = codexAction{Tool: "mcp__" + str(item, "server") + "__" + str(item, "tool"), Input: codexObject(item["arguments"]), Failed: failed}
		result, _ := item["result"].(map[string]any)
		action.Output = codexContentText(result["content"])
		if isError, _ := result["isError"].(bool); isError {
			action.Failed = true
		}
		if action.Output == "" {
			action.Output = timelineValue(item["error"])
		}
	case "CollabAgentToolCall":
		action = codexAction{Tool: str(item, "tool"), Input: map[string]any{}, Failed: failed, Output: codexAgentStates(item)}
		for _, key := range []string{"prompt", "model"} {
			if value := str(item, key); value != "" {
				action.Input[key] = value
			}
		}
	case "FileChange":
		action = codexAction{Tool: "apply_patch", Input: map[string]any{"input": codexPatch(item["changes"])}, Output: str(item, "stdout"), Failed: failed}
	case "WebSearch":
		action = codexAction{Tool: "web_search", Input: map[string]any{"query": codexSearchQuery(item)}, Output: codexSearchResults(item["results"]), Failed: failed}
	case "Extension":
		if str(item, "kind") == "web.search" {
			action = codexAction{Tool: "web_search", Input: map[string]any{"query": codexSearchQuery(item)}, Output: codexSearchResults(item["results"]), Failed: failed}
		} else {
			action = codexAction{Tool: str(item, "kind"), Input: map[string]any{}, Failed: failed}
		}
	case "ImageView":
		action = codexAction{Tool: "view_image", Input: map[string]any{"path": strings.TrimPrefix(str(item, "path"), "file://")}, Failed: failed}
	case "FunctionCallOutput":
		action = codexAction{Tool: strings.Trim(str(item, "namespace")+"."+str(item, "name"), "."), Input: map[string]any{}, Output: timelineValue(item["output"]), Failed: failed}
	default:
		return codexAction{}, false, false
	}
	if action.Tool == "" {
		return codexAction{}, false, false
	}
	return action, true, false
}

func codexExecutedCommand(item map[string]any) string {
	if command, ok := item["command"].([]any); ok && len(command) == 3 {
		if flag, _ := command[1].(string); flag == "-lc" || flag == "-c" {
			if script, ok := command[2].(string); ok {
				return strings.TrimSpace(script)
			}
		}
	}
	if parsed, ok := item["parsed_cmd"].([]any); ok && len(parsed) == 1 {
		if first, ok := parsed[0].(map[string]any); ok && str(first, "cmd") != "" {
			return strings.TrimSpace(str(first, "cmd"))
		}
	}
	switch command := item["command"].(type) {
	case string:
		return strings.TrimSpace(command)
	case []any:
		parts := make([]string, 0, len(command))
		for _, part := range command {
			parts = append(parts, fmt.Sprint(part))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func codexItemDuration(item map[string]any) time.Duration {
	duration, _ := item["duration"].(map[string]any)
	seconds, _ := duration["secs"].(float64)
	nanos, _ := duration["nanos"].(float64)
	return time.Duration(seconds)*time.Second + time.Duration(nanos)
}

func codexFirstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := str(item, key); value != "" {
			return value
		}
	}
	return ""
}

func codexObject(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func codexContentText(value any) string {
	blocks, _ := value.([]any)
	var parts []string
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if text := str(block, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func codexAgentStates(item map[string]any) string {
	names := map[string]string{}
	agents, _ := item["receiver_agents"].([]any)
	for _, raw := range agents {
		agent, _ := raw.(map[string]any)
		names[str(agent, "thread_id")] = str(agent, "agent_nickname")
	}
	states, _ := item["agents_states"].(map[string]any)
	var lines []string
	for _, id := range slices.Sorted(maps.Keys(states)) {
		name := names[id]
		if name == "" {
			name = id
		}
		lines = append(lines, name+": "+strings.ReplaceAll(fmt.Sprint(states[id]), "_", " "))
	}
	return strings.Join(lines, "\n")
}

func codexPatch(value any) string {
	changes, _ := value.(map[string]any)
	var patch strings.Builder
	patch.WriteString("*** Begin Patch\n")
	for _, path := range slices.Sorted(maps.Keys(changes)) {
		change, _ := changes[path].(map[string]any)
		switch str(change, "type") {
		case "add":
			patch.WriteString("*** Add File: " + path + "\n")
			for _, line := range strings.SplitAfter(str(change, "content"), "\n") {
				if line != "" {
					patch.WriteString("+" + strings.TrimSuffix(line, "\n") + "\n")
				}
			}
		case "delete":
			patch.WriteString("*** Delete File: " + path + "\n")
		default:
			patch.WriteString("*** Update File: " + path + "\n")
			if diff := str(change, "unified_diff"); diff != "" {
				patch.WriteString(strings.TrimSuffix(diff, "\n") + "\n")
			}
		}
	}
	patch.WriteString("*** End Patch")
	return patch.String()
}

func codexSearchQuery(item map[string]any) string {
	if query := str(item, "query"); query != "" {
		return query
	}
	action, _ := item["action"].(map[string]any)
	if query := str(action, "query"); query != "" {
		return query
	}
	queries, _ := action["queries"].([]any)
	var parts []string
	for _, query := range queries {
		if text, ok := query.(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " · ")
}

func codexSearchResults(value any) string {
	results, _ := value.([]any)
	var lines []string
	for _, raw := range results {
		result, _ := raw.(map[string]any)
		if title, url := str(result, "title"), str(result, "url"); title != "" || url != "" {
			lines = append(lines, strings.TrimSpace(title+" "+url))
		}
	}
	return strings.Join(lines, "\n")
}
