package surfaces

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

const timelineReadBudget = 4 << 20
const timelineTextBudget = 16 << 10
const timelineItemLimit = 200

func (c *Claude) Timeline(ctx context.Context, session *surface.Session, before int64) (*surface.SessionTimeline, error) {
	read, err := c.ReadSession(ctx, session, surface.SessionReadRequest{Before: before})
	if err != nil {
		return nil, err
	}
	return timelineOf(read), nil
}

func (c *Claude) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	path := session.Transcript
	if path == "" {
		path = c.transcriptPath(session)
	}
	page, err := readTranscriptPage(ctx, path, "claude", request.Before)
	if err != nil {
		return nil, err
	}
	return surface.BoundSessionRead(session, page, request.Limit), nil
}

func (c *Codex) Timeline(ctx context.Context, session *surface.Session, before int64) (*surface.SessionTimeline, error) {
	page, err := readTranscriptPage(ctx, codexTranscriptPath(session), "codex", before)
	if err != nil {
		return nil, err
	}
	return timelineOf(page), nil
}

// ReadSession serves exchanges from the native app-server first and falls back to the bounded
// local transcript page when that read fails; timeline items always come from the local transcript.
func (c *Codex) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	page, err := readTranscriptPage(ctx, codexTranscriptPath(session), "codex", request.Before)
	if err != nil {
		return nil, err
	}
	local := surface.BoundSessionRead(session, page, request.Limit)
	if request.Before > 0 {
		return local, nil
	}
	remote, remoteErr := c.readSessionFromRPC(ctx, session, request.Limit)
	if remoteErr == nil {
		remote.Items = local.Items
		remote.NextBefore = local.NextBefore
		remote.Truncated = local.Truncated
		remote.UnavailableReason = local.UnavailableReason
		return remote, nil
	}
	failure := codexNativeReadFailure(remoteErr)
	if local.UnavailableReason != "" {
		local.UnavailableReason += " " + failure
	} else {
		local.Warning = failure + " Showing the local transcript instead."
	}
	return local, nil
}

func codexNativeReadFailure(err error) string {
	if isCodexTimeout(err) {
		return "Codex Desktop did not answer the native session read in time."
	}
	return "Codex Desktop native session read failed: " + strings.SplitN(err.Error(), "\n", 2)[0] + "."
}

func timelineOf(read *surface.SessionReadResult) *surface.SessionTimeline {
	return &surface.SessionTimeline{Items: read.Items, NextBefore: read.NextBefore, Source: read.Source, Truncated: read.Truncated, UnavailableReason: read.UnavailableReason}
}

type transcriptRecord struct {
	line  []byte
	items []surface.TimelineItem
}

func readTranscriptPage(ctx context.Context, path, source string, before int64) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Source: "local-transcript"}
	if path == "" {
		result.UnavailableReason = "Detailed activity is unavailable because this session has no local transcript."
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.UnavailableReason = "Detailed activity is unavailable because the local transcript has not been created yet."
			return result, nil
		}
		return nil, fmt.Errorf("local activity transcript is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	end := info.Size()
	if before > 0 {
		if before > end {
			return nil, fmt.Errorf("activity transcript changed; refresh the session")
		}
		end = before
	}
	start := max(int64(0), end-timelineReadBudget)
	windowStart := start
	data := make([]byte, end-start)
	if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
		return nil, err
	}
	if start > 0 {
		if first := bytes.IndexByte(data, '\n'); first >= 0 {
			start += int64(first + 1)
			data = data[first+1:]
		} else {
			result.Truncated = true
			result.NextBefore = start
			return result, nil
		}
	}
	// Incomplete appends are retried by the next refresh, never rendered as records.
	if last := bytes.LastIndexByte(data, '\n'); last >= 0 {
		data = data[:last+1]
	} else {
		data = nil
	}
	lines := bytes.SplitAfter(data, []byte{'\n'})
	responseUsers := map[string]bool{}
	if source == "codex" {
		for _, line := range lines {
			var record map[string]any
			if json.Unmarshal(line, &record) != nil || str(record, "type") != "response_item" || str(record, "timestamp") == "" {
				continue
			}
			for _, item := range codexTimelineItems(record) {
				if item.Role == "user" {
					responseUsers[str(record, "timestamp")+"\x00"+item.Text] = true
				}
			}
		}
	}
	offset := start + int64(len(data))
	budget := 512 << 10
	var groups [][]surface.TimelineItem
	var records []transcriptRecord
	for index := len(lines) - 1; index >= 0; index-- {
		line := lines[index]
		offset -= int64(len(line))
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			result.Truncated = true
			continue
		}
		var items []surface.TimelineItem
		if source == "claude" {
			items = claudeTimelineItems(record)
		} else {
			items = codexTimelineItems(record)
		}
		if source == "codex" && str(record, "type") == "event_msg" && len(items) == 1 && items[0].Role == "user" && responseUsers[str(record, "timestamp")+"\x00"+items[0].Text] {
			continue
		}
		if len(items) == 0 {
			continue
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(line))[:12]
		for i := range items {
			items[i].ID = fmt.Sprintf("%d-%s-%d", offset, digest, i)
			items[i].Timestamp = str(record, "timestamp")
		}
		full := slices.Clone(items)
		for i := range items {
			item := &items[i]
			if len(item.Text) > timelineTextBudget {
				cut := timelineTextBudget
				for cut > 0 && !utf8.RuneStart(item.Text[cut]) {
					cut--
				}
				item.Text = item.Text[:cut]
				item.Truncated = true
			}
		}
		encoded, _ := json.Marshal(items)
		count := 0
		for _, group := range groups {
			count += len(group)
		}
		if len(groups) > 0 && (len(encoded) > budget || count+len(items) > timelineItemLimit) {
			result.NextBefore = offset + int64(len(line))
			break
		}
		records = append(records, transcriptRecord{line: line, items: full})
		// A single oversized record must not defeat the response budget.
		for len(encoded) > budget && len(items) > 1 {
			items = items[1:]
			result.Truncated = true
			encoded, _ = json.Marshal(items)
		}
		if len(encoded) > budget {
			result.Truncated = true
			continue
		}
		for _, item := range items {
			result.Truncated = result.Truncated || item.Truncated
		}
		groups = append(groups, items)
		budget -= len(encoded)
	}
	if result.NextBefore == 0 && start > 0 {
		if start == end {
			result.Truncated = true
			result.NextBefore = windowStart
		} else {
			result.NextBefore = start
		}
	}
	for i := len(groups) - 1; i >= 0; i-- {
		result.Items = append(result.Items, groups[i]...)
	}
	slices.Reverse(records)
	if source == "claude" {
		result.Exchanges = claudeTranscriptExchanges(records)
	} else {
		result.Exchanges = codexTranscriptExchanges(records)
	}
	return result, nil
}

func claudeTranscriptExchanges(records []transcriptRecord) []surface.Exchange {
	var turns []claudeTurn
	for _, record := range records {
		var parsed claudeRecord
		if json.Unmarshal(record.line, &parsed) != nil {
			continue
		}
		turns = appendClaudeTurn(turns, parsed)
	}
	exchanges := make([]surface.Exchange, 0, len(turns))
	for _, turn := range turns {
		if turn.User == "" && turn.Assistant == "" {
			continue
		}
		exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant, Timestamp: turn.StartedAt})
	}
	return exchanges
}

func codexTranscriptExchanges(records []transcriptRecord) []surface.Exchange {
	exchanges := make([]surface.Exchange, 0)
	for _, record := range records {
		for _, item := range record.items {
			if item.Kind != "message" || item.Text == "" {
				continue
			}
			timestamp, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
			switch item.Role {
			case "user":
				exchanges = append(exchanges, surface.Exchange{User: item.Text, Timestamp: timestamp})
			case "assistant":
				if len(exchanges) == 0 || exchanges[len(exchanges)-1].Assistant != "" {
					exchanges = append(exchanges, surface.Exchange{Assistant: item.Text, Timestamp: timestamp})
				} else {
					exchanges[len(exchanges)-1].Assistant = item.Text
				}
			}
		}
	}
	return exchanges
}

func timelineValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}

func claudeTimelineItems(record map[string]any) []surface.TimelineItem {
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
			return []surface.TimelineItem{{Kind: "event", Title: "Local command", Text: str(record, "content")}}
		}
		return nil
	}
	if kind != "user" && kind != "assistant" {
		return nil
	}
	message, _ := record["message"].(map[string]any)
	if text, ok := message["content"].(string); ok && text != "" {
		if callID := str(message, "tool_use_id"); callID != "" {
			return []surface.TimelineItem{{Kind: "toolResult", Title: "Tool result", Text: text, CallID: callID}}
		}
		return []surface.TimelineItem{{Kind: "message", Role: kind, Title: kind, Text: text}}
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
			item.Text = timelineValue(block["content"])
			if failed, _ := block["is_error"].(bool); failed {
				item.Status = "error"
			}
		case "image":
			item.Kind = "attachment"
			item.Title = "Image attachment"
			item.Text = "Open the original agent app to view this image."
		default:
			continue
		}
		items = append(items, item)
	}
	return items
}

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
		if images, ok := payload["images"].([]any); ok && len(images) > 0 {
			text += "\n[Image attachment: open the original agent app to view]"
		}
		return []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: text}}
	}
	if str(record, "type") != "response_item" {
		return nil
	}
	item := surface.TimelineItem{}
	switch str(payload, "type") {
	case "message":
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
			if text := str(block, "text"); text != "" {
				parts = append(parts, text)
			} else if strings.Contains(str(block, "type"), "image") {
				parts = append(parts, "[Image attachment: open the original agent app to view]")
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
	return []surface.TimelineItem{item}
}
