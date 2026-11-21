package codexsurface

import (
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func codexRecordTurnID(record map[string]any) string {
	payload, _ := record["payload"].(map[string]any)
	for _, value := range []string{transcript.String(payload, "turn_id"), transcript.String(payload, "turnId"), transcript.String(record, "turn_id"), transcript.String(record, "turnId")} {
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
	if transcript.String(record, "type") == "event_msg" && transcript.String(payload, "type") == "user_message" {
		p.pending = true
		p.turnID = turnID
		return false
	}
	if transcript.String(record, "type") != "response_item" || !hasUserTimelineItem(items) {
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
	typ := transcript.String(payload, "type")
	if status, ended := codexTurnEnd(payload); ended {
		done := codexDoneItem(status, payload)
		return &done
	}
	if typ == "" || (transcript.String(record, "type") != "response_item" && transcript.String(record, "type") != "event_msg") {
		return nil
	}
	switch typ {
	case "message", "agent_message", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "web_search_call", "user_message", "token_count", "task_started", "turn_started":
		return nil
	case "thread_goal_updated":
		notice := codexGoalNotice(payload)
		return &notice
	}
	if slices.Contains(codexUnservedEvents, typ) {
		return nil
	}
	unknown := codexUnknownItem(typ)
	return &unknown
}

func codexServedItemKey(record map[string]any, line []byte, offset int64, item surface.TimelineItem, index int) string {
	if item.ID != "" {
		return item.ID
	}
	key := codexTranscriptItemKey(record, item, index)
	if key == "" || item.Kind == "event" {
		key = codexTranscriptFallbackKey(offset, line, index)
	}
	return key
}

func codexTranscriptFallbackKey(offset int64, line []byte, index int) string {
	return fmt.Sprintf("codex:transcript:%d:%x:%d", offset, sha256.Sum256(line), index)
}

func codexTranscriptItemKey(record map[string]any, item surface.TimelineItem, index int) string {
	payload, _ := record["payload"].(map[string]any)
	semanticID := transcript.String(payload, "id")
	if semanticID == "" {
		semanticID = item.CallID
	}
	if semanticID == "" {
		semanticID = transcript.String(payload, "call_id")
	}
	if semanticID == "" {
		semanticID = transcript.String(record, "uuid")
	}
	if semanticID == "" {
		return ""
	}
	turnID := transcript.String(payload, "turn_id")
	if turnID == "" {
		turnID = transcript.String(payload, "turnId")
	}
	if turnID == "" {
		turnID = transcript.String(record, "turn_id")
	}
	if turnID == "" {
		turnID = transcript.String(record, "turnId")
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
