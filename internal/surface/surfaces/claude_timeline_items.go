package surfaces

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// claudeRecordTimelineItems returns the timeline items of one transcript
// record at offset, in its turn, after the done items of the turns it ended.
// The live stream ends an interrupted turn with its cancelled done alone.
func claudeRecordTimelineItems(ctx context.Context, record map[string]any, offset int64, line []byte, turnID string, ended []claudeTurnEnd, interrupted bool) ([]surface.TimelineItem, error) {
	var items []surface.TimelineItem
	if !interrupted {
		items = claudeTimelineItems(record)
	}
	if err := decorateTimelineAttachments(ctx, items, record, "claude", offset); err != nil {
		return nil, err
	}
	at := str(record, "timestamp")
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = stableTimelineItemID(offset, line, i)
		}
		items[i].TurnID = turnID
		items[i].Timestamp = at
	}
	if len(ended) == 0 {
		return items, nil
	}
	done := make([]surface.TimelineItem, 0, len(ended)+len(items))
	for _, end := range ended {
		done = append(done, surface.TimelineItem{ID: end.Key, Kind: "done", TurnID: end.TurnID, Status: end.Status, Text: end.Reason, Timestamp: at, Turn: end.Turn})
	}
	return append(done, items...), nil
}

// claudeTranscriptReply returns the reply of the latest turn in records that
// has one, and the index of that turn, or -1.
func claudeTranscriptReply(records []transcriptRecord, source string, settled func(time.Time) bool) (*surface.ReplyResult, int) {
	var turns []claudeTurn
	for _, record := range records {
		var parsed claudeRecord
		if json.Unmarshal(record.line, &parsed) == nil {
			turns = appendClaudeTurn(turns, parsed)
		}
	}
	reply := &surface.ReplyResult{Done: false, Source: source}
	from := -1
	for index, turn := range turns {
		if turn.Assistant != "" || turn.APIError != "" {
			from = index
			done := turn.Done || turn.Closing && settled != nil && settled(turn.TerminalAt)
			reply = &surface.ReplyResult{Text: turn.Assistant, UserText: turn.User, Done: done, Source: source}
			if turn.Interrupted && !done {
				reply.Error = "turn interrupted before completion"
			}
			if err := claudeTurnError(turn); err != "" {
				reply.Error = err
			}
		}
	}
	return reply, from
}

func claudeRecordItems(record map[string]any) []surface.TimelineItem {
	kind := str(record, "type")
	if kind == "system" {
		subtype := str(record, "subtype")
		switch subtype {
		case "turn_duration":
			text := str(record, "content")
			if milliseconds, ok := record["durationMs"].(float64); ok && milliseconds >= 0 {
				text = (time.Duration(milliseconds) * time.Millisecond).Round(time.Second).String()
			}
			return []surface.TimelineItem{{Kind: "event", Title: "Turn duration", Text: text}}
		case "compact_boundary":
			return []surface.TimelineItem{{Kind: "event", Title: "Context compacted", Text: str(record, "content")}}
		case "local_command":
			return []surface.TimelineItem{claudeLocalCommandItem(str(record, "content"))}
		}
		return nil
	}
	if kind == "attachment" {
		return claudeQueuedCommandItems(record)
	}
	if kind != "user" && kind != "assistant" {
		return nil
	}
	if apiError, _ := record["isApiErrorMessage"].(bool); apiError && kind == "assistant" {
		return []surface.TimelineItem{claudeAPIErrorNotice(record)}
	}
	message, _ := record["message"].(map[string]any)
	if kind == "user" && claudeImageSourceCompanion(record, message) {
		return nil
	}
	if summary, _ := record["isCompactSummary"].(bool); summary && kind == "user" {
		return []surface.TimelineItem{{Kind: "event", Title: "Compaction summary", Text: claudeMessageText(message["content"])}}
	}
	if kind == "user" {
		origin, _ := record["origin"].(map[string]any)
		if text := claudeMessageText(message["content"]); text != "" && str(message, "tool_use_id") == "" {
			if injected := claudePromptOrigin(origin, text); injected != claudeOriginHuman {
				return []surface.TimelineItem{claudeInjectedItem(injected, origin, text)}
			}
			if claudeCommandMarkup(text) {
				if str(origin, "kind") != claudeOriginHuman {
					return claudeCommandItems(text)
				}
				if claudeCommandInvocation(text) {
					return []surface.TimelineItem{{Kind: "message", Role: kind, Title: kind, Text: claudeCommandLine(text)}}
				}
			}
		}
		// Claude Code writes its own instructions to the model, such as a
		// goal's Stop hook or a loaded skill, as meta records; they show as one
		// line rather than as something the user said.
		if meta, _ := record["isMeta"].(bool); meta && !claudeRecordHasToolResult(record) {
			return []surface.TimelineItem{{Kind: "event", Title: "Claude Code instructions"}}
		}
	}
	origin, _ := record["origin"].(map[string]any)
	typed := str(origin, "kind") == claudeOriginHuman
	if text, ok := message["content"].(string); ok && text != "" {
		if callID := str(message, "tool_use_id"); callID != "" {
			return []surface.TimelineItem{{Kind: "toolResult", Title: "Tool result", Text: text, CallID: callID}}
		}
		item := surface.TimelineItem{Kind: "message", Role: kind, Title: kind, Text: text}
		if kind == "user" && !typed {
			item.Origin = claudeRecordedOrigin(text)
		}
		return []surface.TimelineItem{item}
	}
	blocks, _ := message["content"].([]any)
	var items []surface.TimelineItem
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		item := surface.TimelineItem{Role: kind}
		switch str(block, "type") {
		case "text":
			item.Kind = "message"
			item.Title = kind
			item.Text = str(block, "text")
			if kind == "user" && !typed {
				item.Origin = claudeRecordedOrigin(item.Text)
			}
		case "thinking":
			item.Kind = "reasoning"
			item.Title = "Thinking"
			item.Text = str(block, "thinking")
		case "tool_use":
			item.Kind = "toolCall"
			item.Title = str(block, "name")
			item.CallID = str(block, "id")
			item.Text = timelineValue(block["input"])
		case "tool_result":
			item.Kind = "toolResult"
			item.Title = "Tool result"
			item.CallID = str(block, "tool_use_id")
			item.Text = timelineToolResultText(block["content"])
			if failed, _ := block["is_error"].(bool); failed {
				item.Status = "error"
			}
			if images := toolResultImageContent(block["content"]); item.Text == "" && item.Status == "" && len(images) > 0 {
				for range images {
					items = append(items, surface.TimelineItem{Kind: "attachment", Role: kind, Title: "Image", Text: "Image attachment", CallID: item.CallID})
				}
				continue
			}
		case "image":
			item.Kind = "attachment"
			item.Title = "Image"
			item.Text = "Image attachment"
		default:
			continue
		}
		items = append(items, item)
		if str(block, "type") == "tool_result" {
			for range toolResultImageContent(block["content"]) {
				items = append(items, surface.TimelineItem{Kind: "attachment", Role: kind, Title: "Image", Text: "Image attachment", CallID: str(block, "tool_use_id")})
			}
		}
	}
	return items
}
