package surfaces

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexBackgroundLimit = 16

// codexRecordItems serves one transcript record in order. The window joins a
// tool call, the actions Codex records while it runs and its output into one
// row, and turns a command that outlived its call into a task notice.
func codexRecordItems(window *surface.CodexToolWindow, record map[string]any, turnID string) []surface.TimelineItem {
	payload, _ := record["payload"].(map[string]any)
	if codexRecordStartsTurn(record) {
		window.Known = true
		codexCloseWindow(window)
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
		codexCloseWindow(window)
		return items
	}
	if str(record, "type") != "response_item" || len(items) == 0 {
		return items
	}
	switch str(payload, "type") {
	case "function_call", "custom_tool_call":
		codexOpenWindow(window, record, items[0], turnID)
	case "function_call_output", "custom_tool_call_output":
		if items[0].CallID != "" && items[0].CallID == window.CallID {
			return codexWindowOutput(window, items)
		}
	}
	return items
}

func codexOpenWindow(window *surface.CodexToolWindow, record map[string]any, call surface.TimelineItem, turnID string) {
	payload, _ := record["payload"].(map[string]any)
	window.CallID, window.CallKey, window.TurnID = call.CallID, codexTranscriptItemKey(record, call, 0), turnID
	window.Consumed, window.Failed = false, false
	if calls, ok := codexCodeModeScript(payload); ok {
		window.Commands = codexScriptCommands(calls)
	} else if call.Title == "exec_command" {
		var input map[string]any
		_ = json.Unmarshal([]byte(call.Text), &input)
		window.Commands = nil
		if command := strings.TrimSpace(str(input, "cmd")); command != "" {
			window.Commands = []string{command}
		}
	} else {
		window.Commands = nil
	}
}

func codexCloseWindow(window *surface.CodexToolWindow) {
	window.CallID, window.CallKey, window.TurnID = "", "", ""
	window.Commands, window.Consumed, window.Failed = nil, false, false
}

func codexWindowOpen(window *surface.CodexToolWindow, turnID string) bool {
	return window.CallID != "" && (window.TurnID == "" || turnID == "" || window.TurnID == turnID)
}

func codexCompletedItems(window *surface.CodexToolWindow, item map[string]any, turnID string) []surface.TimelineItem {
	action, ok, mirrored := codexCompletedAction(item)
	if mirrored {
		return nil
	}
	if !ok {
		return []surface.TimelineItem{codexUnknownItem(str(item, "type"))}
	}
	if codexWindowOpen(window, turnID) {
		window.Commands = slices.DeleteFunc(slices.Clone(window.Commands), func(command string) bool { return command == action.Command })
		window.Failed = window.Failed || action.Failed
		if !window.Consumed {
			window.Consumed = true
			call := surface.TimelineItem{ID: window.CallKey, Kind: "toolCall", Title: action.Tool, Text: codexActionInput(action), CallID: window.CallID}
			return []surface.TimelineItem{call, codexActionResult(action, window.CallID)}
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
	encoded, _ := json.Marshal(action.Input)
	return string(encoded)
}

func codexActionResult(action codexAction, callID string) surface.TimelineItem {
	result := surface.TimelineItem{Kind: "toolResult", Title: "Tool result", Text: action.Output, CallID: callID}
	if action.Failed {
		result.Status = "failed"
	}
	return result
}

func codexWindowOutput(window *surface.CodexToolWindow, items []surface.TimelineItem) []surface.TimelineItem {
	consumed, failed, commands, callID := window.Consumed, window.Failed, window.Commands, window.CallID
	codexCloseWindow(window)
	output := items[0]
	if consumed {
		if output.Status == "failed" && !failed {
			return []surface.TimelineItem{{Kind: "notice", Title: "Script failed", Text: output.Text, Status: "failed"}}
		}
		return nil
	}
	if len(commands) > 0 && output.Status != "failed" {
		output.Status = codexBackgroundStatus
		for _, command := range commands {
			window.Background = append(window.Background, surface.CodexBackgroundCommand{CallID: callID, Command: command})
		}
		if extra := len(window.Background) - codexBackgroundLimit; extra > 0 {
			window.Background = slices.Clone(window.Background[extra:])
		}
	}
	if output.Status == codexBackgroundStatus {
		output.Text = strings.TrimSpace("Running in the background.\n\n" + output.Text)
	}
	return append([]surface.TimelineItem{output}, items[1:]...)
}
