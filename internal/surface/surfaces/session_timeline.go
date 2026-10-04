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

const timelineReadBudget = 16 << 20
const timelineTextBudget = 16 << 10
const timelineItemLimit = 200

func (c *Claude) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	path := session.Transcript
	if path == "" {
		path = c.transcriptPath(session)
	}
	page, err := readTranscriptPage(ctx, path, "claude", request.Before, request.Limit)
	if err != nil {
		return nil, err
	}
	return surface.BoundSessionRead(session, page), nil
}

// ReadSession uses the local transcript for Desktop content and the managed app-server for
// managed exchanges; timeline items always come from the local transcript.
func (c *Codex) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	page, err := readTranscriptPage(ctx, codexTranscriptPath(session), "codex", request.Before, request.Limit)
	if err != nil {
		return nil, err
	}
	local := surface.BoundSessionRead(session, page)
	if request.Before > 0 {
		return local, nil
	}
	if !c.managed {
		return local, nil
	}
	remote, remoteErr := c.readSessionFromRPC(ctx, session, request.Limit)
	if remoteErr == nil {
		remote.Items = local.Items
		remote.NextBefore = local.NextBefore
		remote.Truncated = local.Truncated
		remote.UnavailableReason = local.UnavailableReason
		remote.TranscriptOffset = local.TranscriptOffset
		remote.TranscriptOffsetSet = local.TranscriptOffsetSet
		remote.CodexUsesEventUsers = local.CodexUsesEventUsers
		remote.CodexUsesEventUsersSet = local.CodexUsesEventUsersSet
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

func completeJSONLEnd(ctx context.Context, file *os.File, end int64) (int64, error) {
	if end == 0 {
		return 0, nil
	}
	const chunkSize int64 = 64 << 10
	remaining := int64(maxCodexTranscriptRecordBytes)
	position := end
	for position > 0 && remaining > 0 {
		readSize := minInt64(chunkSize, position)
		if readSize > remaining {
			readSize = remaining
		}
		start := position - readSize
		data := make([]byte, readSize)
		if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
			return 0, err
		}
		if index := bytes.LastIndexByte(data, '\n'); index >= 0 {
			return start + int64(index+1), nil
		}
		position = start
		remaining -= readSize
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
	}
	return 0, nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (c *Codex) streamTranscript(ctx context.Context, session *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path := codexTranscriptPath(session)
	if path == "" {
		return fmt.Errorf("Codex local transcript is unavailable")
	}
	offset := session.TranscriptOffset
	deadline := time.Now().Add(timeout)
	currentTurnID := ""
	for time.Now().Before(deadline) {
		lineOffset := offset
		next, err := scanAppendedJSONL(ctx, path, offset, maxCodexTranscriptRecordBytes, func(line []byte) error {
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				return nil
			}
			if turnID := codexRecordTurnID(record); turnID != "" {
				currentTurnID = turnID
			}
			if !session.CodexUsesEventUsersSet {
				usesEventUsers, modeErr := codexTranscriptUsesEventUsers(ctx, path, offset)
				if modeErr != nil {
					return modeErr
				}
				session.CodexUsesEventUsers = usesEventUsers
				session.CodexUsesEventUsersSet = true
			}
			items := codexTimelineItems(record)
			if session.CodexUsesEventUsers && str(record, "type") == "response_item" && hasUserTimelineItem(items) {
				lineOffset += int64(len(line))
				return nil
			}
			if len(items) == 0 {
				if opaque := codexOpaqueTimelineItem(record); opaque != nil {
					emitCodexTranscriptItem(session, uuid, record, line, lineOffset, 0, *opaque, currentTurnID, onEvent)
				}
				lineOffset += int64(len(line))
				return nil
			}
			if err := decorateTimelineAttachments(ctx, items, record, "codex", lineOffset); err != nil {
				return err
			}
			for index, item := range items {
				emitCodexTranscriptItem(session, uuid, record, line, lineOffset, index, item, currentTurnID, onEvent)
			}
			lineOffset += int64(len(line))
			return nil
		})
		if err != nil {
			return err
		}
		offset = next
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("stream timed out after %s", timeout)
}

func emitCodexTranscriptItem(session *surface.Session, uuid string, record map[string]any, line []byte, offset int64, index int, item surface.TimelineItem, currentTurnID string, onEvent func(surface.StreamEvent)) {
	turnID := codexRecordTurnID(record)
	if turnID == "" {
		turnID = currentTurnID
	}
	if uuid != "" && turnID != "" && turnID != uuid {
		return
	}
	key := codexTranscriptItemKey(record, item, index)
	if key == "" || item.Kind == "event" {
		key = codexTranscriptFallbackKey(offset, line, index)
	}
	at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
	operation := "upsert"
	if item.Kind == "done" {
		operation = "phase"
	}
	onEvent(surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(item.Text)), Operation: operation, Final: true, TurnID: turnID, Role: item.Role, Title: item.Title, CallID: item.CallID, Status: item.Status, Attachment: item.Attachment, Timestamp: at, Kind: item.Kind, Text: item.Text})
}

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

func codexOpaqueTimelineItem(record map[string]any) *surface.TimelineItem {
	payload, _ := record["payload"].(map[string]any)
	typ := str(payload, "type")
	if typ == "task_complete" || typ == "task_completed" || typ == "task_cancelled" || typ == "task_canceled" || typ == "task_aborted" || typ == "turn_completed" || typ == "turn_aborted" || typ == "interrupted" {
		return &surface.TimelineItem{Kind: "done", Title: "Turn complete", Status: typ}
	}
	if typ == "" || (str(record, "type") != "response_item" && str(record, "type") != "event_msg") {
		return nil
	}
	switch typ {
	case "message", "agent_message", "reasoning", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "web_search_call", "user_message", "token_count":
		return nil
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

func codexNativeReadFailure(err error) string {
	if isCodexTimeout(err) {
		return "Codex Desktop did not answer the native session read in time."
	}
	return "Codex Desktop native session read failed."
}

type transcriptRecord struct {
	offset int64
	line   []byte
	items  []surface.TimelineItem
}

// readTranscriptPage returns the newest page of the transcript ending at before. When limit is
// positive the page holds at most that many exchanges plus the activity recorded alongside
// them, and NextBefore always addresses the record preceding the oldest returned one.
func readTranscriptPage(ctx context.Context, path, source string, before int64, limit int) (*surface.SessionReadResult, error) {
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
	completeEnd, err := completeJSONLEnd(ctx, file, end)
	if err != nil {
		return nil, err
	}
	result.TranscriptOffset = completeEnd
	result.TranscriptOffsetSet = true
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
	usesEventUsers := false
	if source == "codex" {
		for _, line := range lines {
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				continue
			}
			payload, _ := record["payload"].(map[string]any)
			if str(record, "type") == "event_msg" && str(payload, "type") == "user_message" {
				usesEventUsers = true
			}
		}
	}
	result.CodexUsesEventUsers = usesEventUsers
	result.CodexUsesEventUsersSet = source == "codex"
	offset := start + int64(len(data))
	budget := 512 << 10
	var groups [][]surface.TimelineItem
	var groupOffsets []int64
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
			if len(items) == 0 {
				if opaque := codexOpaqueTimelineItem(record); opaque != nil {
					items = []surface.TimelineItem{*opaque}
				}
			}
		}
		if err := decorateTimelineAttachments(ctx, items, record, source, offset); err != nil {
			return nil, err
		}
		if source == "codex" && usesEventUsers && str(record, "type") == "response_item" && hasUserTimelineItem(items) {
			continue
		}
		if len(items) == 0 {
			continue
		}
		for i := range items {
			if source == "claude" {
				items[i].ID = stableTimelineItemID(offset, line, i)
			} else if source == "codex" {
				items[i].ID = codexTranscriptItemKey(record, items[i], i)
				if items[i].Kind == "event" {
					items[i].ID = codexTranscriptFallbackKey(offset, line, i)
				}
			}
			if items[i].ID == "" {
				if source == "codex" {
					items[i].ID = codexTranscriptFallbackKey(offset, line, i)
				} else {
					digest := fmt.Sprintf("%x", sha256.Sum256(line))[:12]
					items[i].ID = fmt.Sprintf("%d-%s-%d", offset, digest, i)
				}
			}
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
		records = append(records, transcriptRecord{offset: offset, line: line, items: full})
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
		groupOffsets = append(groupOffsets, offset)
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
	slices.Reverse(records)
	var exchangeOffsets []int64
	if source == "claude" {
		result.Exchanges, exchangeOffsets, result.Reply = claudeTranscriptExchanges(records, result.Source)
	} else {
		result.Exchanges, exchangeOffsets = codexTranscriptExchanges(records)
	}
	pageStart := int64(-1)
	if limit > 0 && len(result.Exchanges) > limit {
		result.Exchanges = result.Exchanges[len(result.Exchanges)-limit:]
		pageStart = exchangeOffsets[len(exchangeOffsets)-limit]
		result.NextBefore = pageStart
	}
	for i := len(groups) - 1; i >= 0; i-- {
		if groupOffsets[i] < pageStart {
			continue
		}
		result.Items = append(result.Items, groups[i]...)
	}
	return result, nil
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
	if kind == "" {
		kind = fmt.Sprintf("item%d", index)
	}
	if turnID == "" {
		return "codex:" + kind + ":" + semanticID
	}
	return codexDesktopStreamKey(turnID, kind, semanticID)
}

func claudeTranscriptExchanges(records []transcriptRecord, source string) ([]surface.Exchange, []int64, *surface.ReplyResult) {
	var turns []claudeTurn
	var turnOffsets []int64
	for _, record := range records {
		var parsed claudeRecord
		if json.Unmarshal(record.line, &parsed) != nil {
			continue
		}
		turns = appendClaudeTurn(turns, parsed)
		for len(turnOffsets) < len(turns) {
			turnOffsets = append(turnOffsets, record.offset)
		}
	}
	exchanges := make([]surface.Exchange, 0, len(turns))
	offsets := make([]int64, 0, len(turns))
	reply := &surface.ReplyResult{Done: false, Source: source}
	for index, turn := range turns {
		if turn.User == "" && turn.Assistant == "" {
			continue
		}
		exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant, Timestamp: turn.StartedAt})
		offsets = append(offsets, turnOffsets[index])
		if turn.Assistant != "" {
			reply = &surface.ReplyResult{Text: turn.Assistant, UserText: turn.User, Done: turn.Done, Source: source}
			if turn.Interrupted && !turn.Done {
				reply.Error = "turn interrupted before completion"
			}
		}
	}
	return exchanges, offsets, reply
}

func codexTranscriptExchanges(records []transcriptRecord) ([]surface.Exchange, []int64) {
	exchanges := make([]surface.Exchange, 0)
	offsets := make([]int64, 0)
	for _, record := range records {
		for _, item := range record.items {
			if item.Kind != "message" || item.Text == "" {
				continue
			}
			timestamp, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
			switch item.Role {
			case "user":
				exchanges = append(exchanges, surface.Exchange{User: item.Text, Timestamp: timestamp})
				offsets = append(offsets, record.offset)
			case "assistant":
				if len(exchanges) == 0 || exchanges[len(exchanges)-1].Assistant != "" {
					exchanges = append(exchanges, surface.Exchange{Assistant: item.Text, Timestamp: timestamp})
					offsets = append(offsets, record.offset)
				} else {
					exchanges[len(exchanges)-1].Assistant = item.Text
				}
			}
		}
	}
	return exchanges, offsets
}

func timelineValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		if strings.HasPrefix(strings.ToLower(text), "data:image/") && strings.Contains(strings.ToLower(text), ";base64,") {
			return "[inline image data omitted]"
		}
		return text
	}
	data, _ := json.MarshalIndent(redactInlineImageData(value, false), "", "  ")
	return string(data)
}

func redactInlineImageData(value any, imageContext bool) any {
	switch typed := value.(type) {
	case map[string]any:
		isImage := imageContext || strings.Contains(strings.ToLower(str(typed, "type")), "image")
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if isImage && (lower == "data" || lower == "bytes" || lower == "base64") {
				out[key] = "[inline image data omitted]"
				continue
			}
			out[key] = redactInlineImageData(child, isImage)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactInlineImageData(child, imageContext)
		}
		return out
	case string:
		if strings.HasPrefix(strings.ToLower(typed), "data:image/") && strings.Contains(strings.ToLower(typed), ";base64,") {
			return "[inline image data omitted]"
		}
	}
	return value
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
			item.Text = timelineToolResultText(block["content"])
			if failed, _ := block["is_error"].(bool); failed {
				item.Status = "error"
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

func toolResultImageContent(value any) []map[string]any {
	content, _ := value.([]any)
	images := make([]map[string]any, 0)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "image" {
			images = append(images, block)
		}
	}
	return images
}

func timelineToolResultText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if content, ok := value.([]any); ok {
		var textParts []string
		textOnly := true
		for _, entry := range content {
			block, isBlock := entry.(map[string]any)
			if isBlock && str(block, "type") == "image" {
				continue
			}
			if isBlock && str(block, "type") == "text" && str(block, "text") != "" {
				textParts = append(textParts, str(block, "text"))
				continue
			}
			textOnly = false
		}
		if textOnly && len(textParts) > 0 {
			return strings.Join(textParts, "\n\n")
		}
	}
	clean := sanitizeToolResultValue(value)
	if clean == nil {
		return ""
	}
	return timelineValue(clean)
}

func sanitizeToolResultValue(value any) any {
	switch value := value.(type) {
	case []any:
		clean := make([]any, 0, len(value))
		for _, entry := range value {
			if block, ok := entry.(map[string]any); ok && str(block, "type") == "image" {
				continue
			}
			if sanitized := sanitizeToolResultValue(entry); sanitized != nil {
				clean = append(clean, sanitized)
			}
		}
		return clean
	case map[string]any:
		if str(value, "type") == "image" {
			return nil
		}
		clean := make(map[string]any, len(value))
		for key, entry := range value {
			if key == "data" && strings.Contains(strings.ToLower(str(value, "media_type")), "image/") {
				continue
			}
			if sanitized := sanitizeToolResultValue(entry); sanitized != nil {
				clean[key] = sanitized
			}
		}
		return clean
	default:
		return value
	}
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
		items := []surface.TimelineItem{{Kind: "message", Role: "user", Title: "user", Text: text}}
		if images, ok := payload["images"].([]any); ok && len(images) > 0 {
			for range images {
				items = append(items, surface.TimelineItem{Kind: "attachment", Role: "user", Title: "Image", Text: "Image attachment"})
			}
		}
		return items
	}
	if str(record, "type") == "event_msg" {
		switch str(payload, "type") {
		case "task_complete", "task_completed", "task_cancelled", "task_canceled", "task_aborted", "turn_completed", "turn_aborted", "interrupted":
			return []surface.TimelineItem{{Kind: "done", Title: "Turn complete", Status: str(payload, "type")}}
		}
	}
	if str(record, "type") != "response_item" {
		return nil
	}
	item := surface.TimelineItem{}
	messageAttachmentCount := 0
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
				messageAttachmentCount++
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
	items := []surface.TimelineItem{item}
	for i := 0; i < messageAttachmentCount; i++ {
		items = append(items, surface.TimelineItem{Kind: "attachment", Role: item.Role, Title: "Image", Text: "Image attachment"})
	}
	return items
}
