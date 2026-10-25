package surfaces

import (
	"crypto/sha256"
	"fmt"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexRecordTurnID(record map[string]any) string {
	payload, _ := record["payload"].(map[string]any)
	for _, value := range []string{str(payload, "turn_id"), str(payload, "turnId"), str(record, "turn_id"), str(record, "turnId")} {
		if value != "" {
			return value
		}
	}
	return ""
}

func hasUserTimelineItem(items []surface.TimelineItem) bool {
	for _, item := range items {
		if item.Role == "user" {
			return true
		}
	}
	return false
}

type codexUserProjection struct {
	pending bool
	turnID  string
}

func (p *codexUserProjection) consume(record map[string]any, items []surface.TimelineItem, turnID string) bool {
	payload, _ := record["payload"].(map[string]any)
	if str(record, "type") == "event_msg" && str(payload, "type") == "user_message" {
		p.pending = true
		p.turnID = turnID
		return false
	}
	if str(record, "type") != "response_item" || !hasUserTimelineItem(items) {
		if p.pending && p.turnID != "" {
			if turnID != "" && turnID != p.turnID {
				p.pending = false
				p.turnID = ""
			}
		}
		return false
	}
	duplicate := p.pending && p.turnID != "" && turnID != "" && p.turnID == turnID
	p.pending = false
	p.turnID = ""
	return duplicate
}

func codexOpaqueTimelineItem(record map[string]any) *surface.TimelineItem {
	payload, _ := record["payload"].(map[string]any)
	typ := str(payload, "type")
	if status, ended := codexTurnEnd(payload); ended {
		done := codexDoneItem(status, payload)
		return &done
	}
	if typ == "" || (str(record, "type") != "response_item" && str(record, "type") != "event_msg") {
		return nil
	}
	switch typ {
	case "message", "agent_message", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "web_search_call", "user_message", "token_count", "task_started", "turn_started":
		return nil
	case "item_started", "item_completed":
		// These mirror response_item records that already render as messages.
		item, _ := payload["item"].(map[string]any)
		switch str(item, "type") {
		case "UserMessage", "AgentMessage", "Reasoning":
			return nil
		}
	}
	text := typ + ": " + timelineValue(payload)
	truncated := false
	if len(text) > timelineTextBudget {
		truncated = true
		text = text[:timelineTextBudget]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += "…"
	}
	return &surface.TimelineItem{Kind: "event", Role: str(payload, "role"), Title: "Codex activity", Text: text, Truncated: truncated, TruncationReason: "timeline text limit"}
}

func codexTranscriptFallbackKey(offset int64, line []byte, index int) string {
	return fmt.Sprintf("codex:transcript:%d:%x:%d", offset, sha256.Sum256(line), index)
}

func codexTranscriptItemKey(record map[string]any, item surface.TimelineItem, index int) string {
	payload, _ := record["payload"].(map[string]any)
	semanticID := str(payload, "id")
	if semanticID == "" {
		semanticID = item.CallID
	}
	if semanticID == "" {
		semanticID = str(payload, "call_id")
	}
	if semanticID == "" {
		semanticID = str(record, "uuid")
	}
	if semanticID == "" {
		return ""
	}
	turnID := str(payload, "turn_id")
	if turnID == "" {
		turnID = str(payload, "turnId")
	}
	if turnID == "" {
		turnID = str(record, "turn_id")
	}
	if turnID == "" {
		turnID = str(record, "turnId")
	}
	kind := codexDesktopKeyKind(item.Kind)
	switch kind {
	case "":
		kind = fmt.Sprintf("item%d", index)
	case "attachment":
		kind = fmt.Sprintf("attachment%d", index)
	}
	if turnID == "" {
		return "codex:" + kind + ":" + semanticID
	}
	return codexDesktopStreamKey(turnID, kind, semanticID)
}
