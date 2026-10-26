package surfaces

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codemode"
)

// codexRecordItems serves one transcript record in order. The window joins a
// tool call, the actions Codex records while it runs and its output into one
// row, and turns a command that outlived its call into a task notice.
func codexRecordItems(window *surface.CodexToolWindow, record map[string]any, turnID string) []surface.TimelineItem {
	payload, _ := record["payload"].(map[string]any)
	if codexRecordStartsTurn(record) {
		window.Known = true
		window.Open = nil
	}
	if str(record, "type") == "event_msg" && str(payload, "type") == "item_completed" {
		item, _ := payload["item"].(map[string]any)
		return codexCompletedItems(window, item, turnID)
	}
	items := codexTimelineItems(record)
	if len(items) == 0 {
		if opaque := codexOpaqueTimelineItem(record); opaque != nil {
			items = []surface.TimelineItem{*opaque}
		}
	}
	if _, ended := codexRecordEndsTurn(record); ended {
		window.Open = nil
		return items
	}
	if str(record, "type") != "response_item" || len(items) == 0 || items[0].CallID == "" {
		return items
	}
	switch str(payload, "type") {
	case "function_call", "custom_tool_call":
		codexOpenCall(window, record, items[0], turnID)
	case "function_call_output", "custom_tool_call_output":
		if index := slices.IndexFunc(window.Open, func(call surface.CodexOpenCall) bool { return call.CallID == items[0].CallID }); index >= 0 {
			return codexCallOutput(window, index, items)
		}
	}
	return items
}

func codexOpenCall(window *surface.CodexToolWindow, record map[string]any, item surface.TimelineItem, turnID string) {
	payload, _ := record["payload"].(map[string]any)
	call := surface.CodexOpenCall{CallID: item.CallID, CallKey: codexTranscriptItemKey(record, item, 0), TurnID: turnID, At: str(record, "timestamp"), Tools: []string{item.Title}}
	if calls, ok := codexCodeModeScript(payload); ok {
		call.Tools = nil
		for _, script := range calls {
			call.Tools = append(call.Tools, codemode.ToolName(script.Tool))
		}
		call.Commands = codexScriptCommands(calls)
	} else if item.Title == "exec_command" {
		var input map[string]any
		_ = json.Unmarshal([]byte(item.Text), &input)
		if command := strings.TrimSpace(str(input, "cmd")); command != "" {
			call.Commands = []string{command}
		}
	}
	window.Open = append(slices.DeleteFunc(slices.Clone(window.Open), func(other surface.CodexOpenCall) bool { return other.CallID == call.CallID }), call)
}

// codexActionCall finds the open call an action belongs to: the one running
// its command, else the one calling its tool, else the only one open.
func codexActionCall(window *surface.CodexToolWindow, action codexAction, turnID string) int {
	candidate := func(call surface.CodexOpenCall) bool {
		return call.TurnID == "" || turnID == "" || call.TurnID == turnID
	}
	if action.Command != "" {
		if index := slices.IndexFunc(window.Open, func(call surface.CodexOpenCall) bool {
			return candidate(call) && slices.Contains(call.Commands, action.Command)
		}); index >= 0 {
			return index
		}
		if slices.ContainsFunc(window.Background, func(command surface.CodexBackgroundCommand) bool { return command.Command == action.Command }) {
			return -1
		}
	}
	if index := slices.IndexFunc(window.Open, func(call surface.CodexOpenCall) bool {
		return candidate(call) && slices.Contains(call.Tools, action.Tool)
	}); index >= 0 {
		return index
	}
	only := -1
	for index, call := range window.Open {
		if candidate(call) {
			if only >= 0 {
				return -1
			}
			only = index
		}
	}
	return only
}

func codexCompletedItems(window *surface.CodexToolWindow, item map[string]any, turnID string) []surface.TimelineItem {
	action, ok, mirrored := codexCompletedAction(item)
	if mirrored {
		return nil
	}
	if !ok {
		return []surface.TimelineItem{codexUnknownItem(str(item, "type"))}
	}
	if index := codexActionCall(window, action, turnID); index >= 0 {
		window.Open = slices.Clone(window.Open)
		call := &window.Open[index]
		call.Commands = slices.DeleteFunc(slices.Clone(call.Commands), func(command string) bool { return command == action.Command })
		call.Failed = call.Failed || action.Failed
		if !call.Consumed {
			call.Consumed = true
			row := surface.TimelineItem{ID: call.CallKey, Kind: "toolCall", Title: action.Tool, Text: codexActionInput(action), CallID: call.CallID, Timestamp: call.At}
			return []surface.TimelineItem{row, codexActionResult(action, call.CallID)}
		}
		return codexActionRow(action, str(item, "id"))
	}
	if action.Tool == "exec_command" && window.Known {
		return []surface.TimelineItem{codexBackgroundFinished(window, action)}
	}
	return codexActionRow(action, str(item, "id"))
}

func codexActionRow(action codexAction, id string) []surface.TimelineItem {
	return []surface.TimelineItem{{Kind: "toolCall", Title: action.Tool, Text: codexActionInput(action), CallID: id}, codexActionResult(action, id)}
}

func codexActionInput(action codexAction) string {
	input := map[string]any{}
	for key, value := range action.Input {
		if value != nil && value != "" {
			input[key] = value
		}
	}
	if len(input) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(input)
	return string(encoded)
}

func codexActionResult(action codexAction, callID string) surface.TimelineItem {
	result := surface.TimelineItem{Kind: "toolResult", Title: "Tool result", Text: action.Output, CallID: callID}
	if action.Failed {
		result.Status = "failed"
	}
	return result
}

func codexCallOutput(window *surface.CodexToolWindow, index int, items []surface.TimelineItem) []surface.TimelineItem {
	call := window.Open[index]
	window.Open = slices.Delete(slices.Clone(window.Open), index, index+1)
	output := items[0]
	if call.Consumed {
		if output.Status == "failed" && !call.Failed {
			return []surface.TimelineItem{{Kind: "notice", Title: "Script failed", Text: output.Text, Status: "failed"}}
		}
		return nil
	}
	if len(call.Commands) > 0 && output.Status != "failed" {
		output.Status = codexBackgroundStatus
		background := slices.Clone(window.Background)
		for _, command := range call.Commands {
			background = append(background, surface.CodexBackgroundCommand{CallID: call.CallID, Command: command})
		}
		window.Background = background
	}
	if output.Status == codexBackgroundStatus {
		output.Text = strings.TrimSpace("Running in the background.\n\n" + output.Text)
	}
	return append([]surface.TimelineItem{output}, items[1:]...)
}
