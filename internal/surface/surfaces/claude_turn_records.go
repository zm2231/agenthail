package surfaces

import (
	"strings"
)

func claudeRecordStartsTurn(record map[string]any) bool {
	compactSummary, _ := record["isCompactSummary"].(bool)
	return str(record, "type") == "user" && str(record, "uuid") != "" && !compactSummary && !claudeRecordHasToolResult(record) && !claudeRecordIsInterrupt(record) && !claudeRecordIsCommand(record)
}

func claudeRecordHasToolResult(record map[string]any) bool {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "tool_result" {
			return true
		}
	}
	return str(message, "tool_use_id") != ""
}

func claudeRecordIsInterrupt(record map[string]any) bool {
	origin, _ := record["origin"].(map[string]any)
	if str(record, "type") != "user" || str(origin, "kind") == claudeOriginHuman {
		return false
	}
	message, _ := record["message"].(map[string]any)
	return isClaudeInterruptMarker(strings.TrimSpace(transcriptText(message["content"])))
}

// claudeStreamTerminal classifies a record: turn_duration ends a turn, an
// end_turn block starts closing it (see appendClaudeTurn), and an interruption
// cancels it.
func claudeStreamTerminal(record map[string]any) (done, ending, interrupted bool) {
	switch str(record, "type") {
	case "system":
		return str(record, "subtype") == "turn_duration", false, false
	case "assistant":
		if apiError, _ := record["isApiErrorMessage"].(bool); apiError {
			return true, false, false
		}
		reason := strNested(record, "message", "stop_reason")
		return false, reason == "end_turn", claudeTerminalInterruption(reason)
	case "user":
		return false, false, claudeRecordIsInterrupt(record)
	default:
		return false, false, false
	}
}
