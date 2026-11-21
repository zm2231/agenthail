package claudesurface

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func claudeRecordStartsTurn(record map[string]any) bool {
	compactSummary, _ := record["isCompactSummary"].(bool)
	return transcript.String(record, "type") == "user" && transcript.String(record, "uuid") != "" && !compactSummary && !claudeRecordHasToolResult(record) && !claudeRecordIsInterrupt(record) && !claudeRecordIsCommand(record)
}

func claudeRecordHasToolResult(record map[string]any) bool {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if transcript.String(block, "type") == "tool_result" {
			return true
		}
	}
	return transcript.String(message, "tool_use_id") != ""
}

func claudeRecordIsInterrupt(record map[string]any) bool {
	origin, _ := record["origin"].(map[string]any)
	if transcript.String(record, "type") != "user" || transcript.String(origin, "kind") == claudeOriginHuman {
		return false
	}
	message, _ := record["message"].(map[string]any)
	return isClaudeInterruptMarker(strings.TrimSpace(transcriptText(message["content"])))
}

// claudeStreamTerminal classifies a record: turn_duration ends a turn, an
// end_turn block starts closing it (see appendClaudeTurn), and an interruption
// cancels it.
func claudeStreamTerminal(record map[string]any) (done, ending, interrupted bool) {
	switch transcript.String(record, "type") {
	case "system":
		return transcript.String(record, "subtype") == "turn_duration", false, false
	case "assistant":
		if apiError, _ := record["isApiErrorMessage"].(bool); apiError {
			return true, false, false
		}
		reason := transcript.NestedString(record, "message", "stop_reason")
		return false, reason == "end_turn", claudeTerminalInterruption(reason)
	case "user":
		return false, false, claudeRecordIsInterrupt(record)
	default:
		return false, false, false
	}
}

// claudeStopHooksEndTurn reports a main-thread stop_hook_summary after which
// Claude stopped. A turn whose last reply was empty has no end_turn block, so
// this is its only end. A blocking Stop hook leaves its feedback in hookErrors
// and the turn goes on.
func claudeStopHooksEndTurn(subtype string, sidechain, prevented bool, hookErrors int) bool {
	return subtype == "stop_hook_summary" && !sidechain && (prevented || hookErrors == 0)
}

func claudeRecordIsStopHookFeedback(record map[string]any) bool {
	meta, _ := record["isMeta"].(bool)
	message, _ := record["message"].(map[string]any)
	return meta && transcript.String(record, "type") == "user" && isClaudeStopHookFeedback(transcriptText(message["content"]))
}

func isClaudeStopHookFeedback(text string) bool {
	return strings.HasPrefix(text, "Stop hook feedback:")
}

func claudeRecordStopsAfterHooks(record map[string]any) bool {
	if transcript.String(record, "type") != "system" {
		return false
	}
	sidechain, _ := record["isSidechain"].(bool)
	prevented, _ := record["preventedContinuation"].(bool)
	hookErrors, _ := record["hookErrors"].([]any)
	return claudeStopHooksEndTurn(transcript.String(record, "subtype"), sidechain, prevented, len(hookErrors))
}

func claudeLineMayStartTurn(line []byte) bool {
	if gjson.GetBytes(line, "type").Str != "user" || gjson.GetBytes(line, "message.tool_use_id").Exists() {
		return false
	}
	return !gjson.GetBytes(line, `message.content.#(type=="tool_result").type`).Exists()
}
