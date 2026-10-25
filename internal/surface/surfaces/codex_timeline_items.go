package surfaces

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexTimelineItems(record map[string]any) []surface.TimelineItem {
	payload, _ := record["payload"].(map[string]any)
	if str(record, "type") == "realtime_item" {
		return codexVoiceTimelineItems(payload)
	}
	if str(record, "type") == "compacted" {
		return []surface.TimelineItem{{Kind: "event", Title: "Context compacted", Text: str(payload, "message")}}
	}
	if str(record, "type") == "event_msg" && str(payload, "type") == "user_message" {
		text := str(payload, "message")
		items := []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: text}}
		if images, ok := payload["images"].([]any); ok && len(images) > 0 {
			for range images {
				items = append(items, surface.TimelineItem{Kind: "attachment", Role: "user", Title: "Image", Text: "Image attachment"})
			}
		}
		return items
	}
	if str(record, "type") == "event_msg" {
		if status, ended := codexTurnEnd(payload); ended {
			done := codexDoneItem(status, payload)
			if status != "failed" {
				return []surface.TimelineItem{done}
			}
			_, info := codexTurnError(payload)
			notice := surface.TimelineItem{Kind: "notice", Title: "Codex error", Text: done.Text, Status: info}
			return []surface.TimelineItem{notice, done}
		}
	}
	if str(record, "type") != "response_item" {
		return nil
	}
	item := surface.TimelineItem{}
	messageAttachmentCount := 0
	switch str(payload, "type") {
	case "message":
		if notice, ok := codexInterruptionNotice(payload); ok {
			return []surface.TimelineItem{notice}
		}
		if text, ok := codexInstructions(payload); ok {
			return []surface.TimelineItem{{Kind: "instructions", Title: "Instructions", Text: text}}
		}
		item.Kind = "message"
		item.Role = str(payload, "role")
		item.Title = item.Role
		if channel := str(payload, "channel"); channel != "" {
			item.Title += " · " + channel
		}
		blocks, _ := payload["content"].([]any)
		var parts []string
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if text := str(block, "text"); codexImageWrapper(text) {
				continue
			} else if text != "" {
				parts = append(parts, text)
			} else if strings.Contains(str(block, "type"), "image") {
				messageAttachmentCount++
			}
		}
		item.Text = strings.Join(parts, "\n\n")
	case "function_call", "custom_tool_call":
		item.Kind = "toolCall"
		item.Title = str(payload, "name")
		item.CallID = str(payload, "call_id")
		item.Text = timelineValue(payload["arguments"])
		if item.Text == "" {
			item.Text = timelineValue(payload["input"])
		}
	case "function_call_output", "custom_tool_call_output":
		item.Kind = "toolResult"
		item.Title = "Tool result"
		item.CallID = str(payload, "call_id")
		if text, images, ok := codexTypedToolOutput(payload["output"]); ok {
			item.Text = text
			items := []surface.TimelineItem{item}
			for i := 0; i < images; i++ {
				items = append(items, surface.TimelineItem{Kind: "attachment", Role: item.Role, Title: "Image", Text: "Image attachment", CallID: item.CallID})
			}
			return items
		}
		item.Text = timelineValue(payload["output"])
	case "reasoning":
		item.Kind = "reasoning"
		item.Title = "Reasoning summary"
		blocks, _ := payload["summary"].([]any)
		var parts []string
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if text := str(block, "text"); text != "" {
				parts = append(parts, text)
			}
		}
		item.Text = strings.Join(parts, "\n\n")
		if item.Text == "" {
			return nil
		}
	case "web_search_call":
		item.Kind = "toolCall"
		item.Title = "Web search"
		item.Text = timelineValue(payload["action"])
		item.Status = str(payload, "status")
	default:
		return nil
	}
	items := []surface.TimelineItem{item}
	if item.Kind == "message" && item.Text == "" && messageAttachmentCount > 0 {
		items = nil
	}
	for i := 0; i < messageAttachmentCount; i++ {
		items = append(items, surface.TimelineItem{Kind: "attachment", Role: item.Role, Title: "Image", Text: "Image attachment"})
	}
	return items
}

// codexInstructions returns the text of a message that is model input
// rather than conversation: a developer message, or a user message made
// only of Codex's injected context. A user message with any typed block
// stays a prompt.
func codexInstructions(payload map[string]any) (string, bool) {
	blocks, _ := payload["content"].([]any)
	var parts []string
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		text := str(block, "text")
		if strings.TrimSpace(text) == "" {
			if str(payload, "role") == "user" && strings.Contains(str(block, "type"), "image") {
				return "", false
			}
			continue
		}
		if str(payload, "role") == "user" && !surface.IsCodexInjectedContext(text) {
			return "", false
		}
		parts = append(parts, text)
	}
	switch str(payload, "role") {
	case "developer", "system":
		return strings.Join(parts, "\n\n"), true
	case "user":
		return strings.Join(parts, "\n\n"), len(parts) > 0
	}
	return "", false
}

func codexTypedToolOutput(value any) (string, int, bool) {
	blocks, ok := value.([]any)
	if !ok || len(blocks) == 0 {
		return "", 0, false
	}
	var text strings.Builder
	images := 0
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			return "", 0, false
		}
		switch str(block, "type") {
		case "input_text":
			part, ok := block["text"].(string)
			if !ok {
				return "", 0, false
			}
			if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") && part != "" {
				text.WriteString("\n")
			}
			text.WriteString(part)
		case "input_image":
			if _, ok := attachmentReferenceFromValue(block); !ok {
				return "", 0, false
			}
			images++
		default:
			return "", 0, false
		}
	}
	return text.String(), images, true
}

// codexImageWrapper reports the text Codex puts around each image input
// ("<image name=[Image #1] path=...>" and "</image>"). The image renders as an
// attachment item, and the path is local to that machine.
func codexImageWrapper(text string) bool {
	text = strings.TrimSpace(text)
	return text == "</image>" || strings.HasPrefix(text, "<image name=[Image #") && strings.HasSuffix(text, ">") && !strings.Contains(text, "\n")
}
