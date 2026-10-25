package surfaces

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexTurnAbortedOpen  = "<turn_aborted>"
	codexTurnAbortedClose = "</turn_aborted>"
)

func codexDoneItem(status string, payload map[string]any) surface.TimelineItem {
	item := surface.TimelineItem{Kind: "done", Title: "Turn complete", Status: status}
	switch status {
	case "task_cancelled", "task_canceled", "task_aborted", "turn_aborted", "interrupted":
		item.Title = "Turn interrupted"
	case "failed":
		item.Title = "Turn failed"
		item.Text, _ = codexTurnError(payload)
	}
	return item
}

// codexTurnError reads the error a failed turn's end record carries. Codex
// writes an object with a message and a codex_error_info kind; any other
// value still failed the turn, so it gets a generic message.
func codexTurnError(payload map[string]any) (message, info string) {
	switch failure := payload["error"].(type) {
	case map[string]any:
		message, info = str(failure, "message"), str(failure, "codex_error_info")
	case string:
		message = failure
	}
	if strings.TrimSpace(message) == "" {
		message = "Codex ended the turn with an error and no message."
	}
	if info == "" {
		info = "error"
	}
	return message, info
}

func codexInterruptionNotice(payload map[string]any) (surface.TimelineItem, bool) {
	if str(payload, "role") != "user" {
		return surface.TimelineItem{}, false
	}
	blocks, _ := payload["content"].([]any)
	if len(blocks) != 1 {
		return surface.TimelineItem{}, false
	}
	block, _ := blocks[0].(map[string]any)
	if str(block, "type") != "input_text" {
		return surface.TimelineItem{}, false
	}
	text := strings.TrimSpace(str(block, "text"))
	if !strings.HasPrefix(text, codexTurnAbortedOpen) || !strings.HasSuffix(text, codexTurnAbortedClose) {
		return surface.TimelineItem{}, false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, codexTurnAbortedOpen), codexTurnAbortedClose))
	if strings.Contains(inner, codexTurnAbortedOpen) || strings.Contains(inner, codexTurnAbortedClose) {
		return surface.TimelineItem{}, false
	}
	return surface.TimelineItem{Kind: "event", Title: "Turn interrupted", Text: inner}, true
}
