package codexsurface

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codemode"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func codexTimelineItems(record map[string]any) []surface.TimelineItem {
	payload, _ := record["payload"].(map[string]any)
	if transcript.String(record, "type") == "realtime_item" {
		return codexVoiceTimelineItems(payload)
	}
	if transcript.String(record, "type") == "compacted" {
		return []surface.TimelineItem{{Kind: "event", Title: "Context compacted", Text: transcript.String(payload, "message")}}
	}
	if transcript.String(record, "type") == "event_msg" && transcript.String(payload, "type") == "user_message" {
		text := transcript.String(payload, "message")
		items := []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: text}}
		if images, ok := payload["images"].([]any); ok && len(images) > 0 {
			for range images {
				items = append(items, surface.TimelineItem{Kind: "attachment", Role: "user", Title: "Image", Text: "Image attachment"})
			}
		}
		return items
	}
	if transcript.String(record, "type") == "event_msg" {
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
	if transcript.String(record, "type") != "response_item" {
		return nil
	}
	item := surface.TimelineItem{}
	messageAttachmentCount := 0
	switch transcript.String(payload, "type") {
	case "message":
		if notice, ok := codexInterruptionNotice(payload); ok {
			return []surface.TimelineItem{notice}
		}
		if text, ok := codexInstructions(payload); ok {
			return []surface.TimelineItem{{Kind: "instructions", Title: "Instructions", Text: text}}
		}
		item.Kind = "message"
		item.Role = transcript.String(payload, "role")
		item.Phase = transcript.String(payload, "phase")
		item.Title = item.Role
		if channel := transcript.String(payload, "channel"); channel != "" {
			item.Title += " · " + channel
		}
		blocks, _ := payload["content"].([]any)
		var parts []string
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if text := transcript.String(block, "text"); codexImageWrapper(text) {
				continue
			} else if text != "" {
				parts = append(parts, text)
			} else if strings.Contains(transcript.String(block, "type"), "image") {
				messageAttachmentCount++
			}
		}
		item.Text = strings.Join(parts, "\n\n")
	case "function_call", "custom_tool_call":
		item.Kind = "toolCall"
		item.Title = transcript.String(payload, "name")
		item.CallID = transcript.String(payload, "call_id")
		if calls, ok := codexCodeModeScript(payload); ok {
			row := codemode.Present(calls)
			item.Title, item.Text = row.Tool, row.Text()
			break
		}
		item.Text = transcript.TimelineValue(payload["arguments"])
		if item.Text == "" {
			item.Text = transcript.TimelineValue(payload["input"])
		}
	case "function_call_output", "custom_tool_call_output":
		item.Kind = "toolResult"
		item.Title = "Tool result"
		item.CallID = transcript.String(payload, "call_id")
		if text, images, ok := codexTypedToolOutput(payload["output"]); ok {
			item.Text = text
			if body, status, ok := codexScriptOutput(text); ok {
				item.Text, item.Status = body, status
			}
			items := []surface.TimelineItem{item}
			for i := 0; i < images; i++ {
				items = append(items, surface.TimelineItem{Kind: "attachment", Role: item.Role, Title: "Image", Text: "Image attachment", CallID: item.CallID})
			}
			return items
		}
		item.Text = transcript.TimelineValue(payload["output"])
	case "reasoning":
		item.Kind = "reasoning"
		item.Title = "Reasoning summary"
		blocks, _ := payload["summary"].([]any)
		var parts []string
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if text := transcript.String(block, "text"); text != "" {
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
		item.Text = transcript.TimelineValue(payload["action"])
		item.Status = transcript.String(payload, "status")
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
		text := transcript.String(block, "text")
		if strings.TrimSpace(text) == "" {
			if transcript.String(payload, "role") == "user" && strings.Contains(transcript.String(block, "type"), "image") {
				return "", false
			}
			continue
		}
		if transcript.String(payload, "role") == "user" && !surface.IsCodexInjectedContext(text) {
			return "", false
		}
		parts = append(parts, text)
	}
	switch transcript.String(payload, "role") {
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
		switch transcript.String(block, "type") {
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
			if _, ok := transcript.AttachmentReferenceFromValue(block); !ok {
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
