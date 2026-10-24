package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const maxClaudeTranscriptRecordBytes = 32 * 1024 * 1024

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

type claudeTurn struct {
	UserID      string
	MessageID   string
	User        string
	Assistant   string
	Model       string
	StartedAt   time.Time
	TerminalAt  time.Time
	Done        bool
	Closing     bool
	Interrupted bool
	// APIError is the message Claude Code wrote in place of a reply the API
	// refused; UsageLimited marks a refusal by a usage limit, which lifts at
	// UsageResetsAt when Claude said when.
	APIError      string
	UsageLimited  bool
	UsageResetsAt time.Time
	// LastAt is the newest record the turn wrote, and Tools the tool calls
	// it started that have no result yet, oldest first.
	LastAt time.Time
	Tools  []claudeToolCall
}

type claudeToolCall struct {
	ID        string
	Name      string
	StartedAt time.Time
}

type claudeRecord struct {
	Type       string `json:"type"`
	UUID       string `json:"uuid"`
	ParentUUID string `json:"parentUuid"`
	CommandRun *struct {
		Command string `json:"command"`
		Args    string `json:"args"`
	} `json:"commandRun"`
	Subtype           string `json:"subtype"`
	Content           string `json:"content"`
	Timestamp         string `json:"timestamp"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	Error             string `json:"error"`
	APIError          string `json:"apiError"`
	QuotaLimits       struct {
		ResetsAt float64 `json:"resetsAt"`
	} `json:"quotaLimits"`
	APIErrorParams struct {
		RateLimitInfo struct {
			ResetsAt float64 `json:"resetsAt"`
		} `json:"rate_limit_info"`
	} `json:"apiErrorParams"`
	Origin struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    any    `json:"content"`
		ToolUseID  string `json:"tool_use_id"`
	} `json:"message"`
}

var claudeModelScanChunkBytes int64 = 256 << 10

// readClaudeLatestModel returns the model of the newest complete assistant
// record within the observation tail. It scans backward and decodes only
// records that mention a model, so a long transcript costs one short read.
func readClaudeLatestModel(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	minimum := max(0, info.Size()-initialClaudeObservationBytes)
	position := info.Size()
	trailing := true
	var carry []byte
	for position > minimum {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		start := max(minimum, position-claudeModelScanChunkBytes)
		data := make([]byte, position-start, position-start+int64(len(carry)))
		if _, err := file.ReadAt(data, start); err != nil && err != io.EOF {
			return "", err
		}
		data = append(data, carry...)
		position = start
		if trailing {
			last := bytes.LastIndexByte(data, '\n')
			if last < 0 {
				carry = nil
				continue
			}
			data, trailing = data[:last+1], false
		}
		lineEnd := len(data)
		for {
			newline := bytes.LastIndexByte(data[:lineEnd], '\n')
			if newline < 0 {
				break
			}
			if model := claudeAssistantModel(data[newline+1 : lineEnd]); model != "" {
				return model, nil
			}
			lineEnd = newline
		}
		if lineEnd > maxClaudeTranscriptRecordBytes {
			return "", fmt.Errorf("transcript record exceeds %d bytes", maxClaudeTranscriptRecordBytes)
		}
		carry = data[:lineEnd]
	}
	if minimum == 0 && !trailing {
		if model := claudeAssistantModel(carry); model != "" {
			return model, nil
		}
	}
	return "", fmt.Errorf("model unavailable: no assistant turn recorded")
}

func claudeAssistantModel(line []byte) string {
	if !bytes.Contains(line, []byte(`"model"`)) {
		return ""
	}
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "assistant" || record.Message.Model == claudeSyntheticModel {
		return ""
	}
	return record.Message.Model
}

// claudeSyntheticModel is the model Claude Code names on a message it writes
// itself, such as an API error in place of a reply.
const claudeSyntheticModel = "<synthetic>"

// appendClaudeTurn folds one transcript record into turns. Claude Code writes
// each content block of an API message as its own record, and every block
// carries the message's stop_reason, so end_turn on a block only marks the
// turn as closing. It closes at the first later record that is not another
// block of the same message, or at turn_duration. An interrupt marker at that
// point interrupts the turn instead of completing it.
func appendClaudeTurn(turns []claudeTurn, record claudeRecord) []claudeTurn {
	turns = foldClaudeTurn(turns, record)
	if len(turns) > 0 {
		noteClaudeTurnActivity(&turns[len(turns)-1], record)
	}
	return turns
}

// noteClaudeTurnActivity records when a turn last wrote and which tool calls
// it is still running. Tools is replaced rather than changed in place, since
// observation snapshots share it.
func noteClaudeTurnActivity(turn *claudeTurn, record claudeRecord) {
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return
	}
	if at.After(turn.LastAt) {
		turn.LastAt = at
	}
	blocks, _ := record.Message.Content.([]any)
	switch record.Type {
	case "assistant":
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if str(block, "type") == "tool_use" && str(block, "id") != "" {
				turn.Tools = append(append([]claudeToolCall(nil), turn.Tools...), claudeToolCall{ID: str(block, "id"), Name: str(block, "name"), StartedAt: at})
			}
		}
	case "user":
		finished := map[string]bool{}
		if record.Message.ToolUseID != "" {
			finished[record.Message.ToolUseID] = true
		}
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if str(block, "type") == "tool_result" {
				finished[str(block, "tool_use_id")] = true
			}
		}
		if len(finished) == 0 || len(turn.Tools) == 0 {
			return
		}
		running := make([]claudeToolCall, 0, len(turn.Tools))
		for _, call := range turn.Tools {
			if !finished[call.ID] {
				running = append(running, call)
			}
		}
		turn.Tools = running
	}
}

// foldClaudeTurn applies one record to the turn sequence; see appendClaudeTurn.
func foldClaudeTurn(turns []claudeTurn, record claudeRecord) []claudeTurn {
	if len(turns) > 0 {
		turn := &turns[len(turns)-1]
		if turn.Closing && !claudeRecordContinuesMessage(record.Type, record.Message.ID, turn.MessageID) {
			turn.Closing = false
			turn.Done = record.Type != "user" || record.Origin.Kind == claudeOriginHuman || !isClaudeInterruptMarker(transcriptText(record.Message.Content))
		}
	}
	switch record.Type {
	case "user":
		text := transcriptText(record.Message.Content)
		if record.Origin.Kind != claudeOriginHuman && isClaudeInterruptMarker(text) {
			if len(turns) > 0 {
				turns[len(turns)-1].Interrupted = true
			}
			return turns
		}
		text, human := claudePromptText(record.Origin.Kind, text)
		if !human || record.Message.ToolUseID != "" {
			return turns
		}
		startedAt, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
		return append(turns, claudeTurn{UserID: record.UUID, User: strings.TrimSpace(text), StartedAt: startedAt})
	case "assistant":
		if len(turns) == 0 {
			turns = append(turns, claudeTurn{})
		}
		turn := &turns[len(turns)-1]
		if turn.MessageID != "" && record.Message.ID != "" && turn.MessageID != record.Message.ID && turn.Done {
			turns = append(turns, claudeTurn{})
			turn = &turns[len(turns)-1]
		}
		if turn.StartedAt.IsZero() {
			turn.StartedAt, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
		}
		if record.Message.ID != "" {
			turn.MessageID = record.Message.ID
		} else if turn.MessageID == "" {
			turn.MessageID = record.UUID
		}
		if record.IsAPIErrorMessage {
			turn.APIError = strings.TrimSpace(transcriptText(record.Message.Content))
			turn.UsageLimited = record.Error == "rate_limit" || record.APIError == "usage_limit_reached"
			turn.UsageResetsAt = time.Time{}
			for _, seconds := range []float64{record.QuotaLimits.ResetsAt, record.APIErrorParams.RateLimitInfo.ResetsAt} {
				if turn.UsageLimited && seconds > 0 {
					turn.UsageResetsAt = time.Unix(int64(seconds), 0).UTC()
					break
				}
			}
			turn.Done, turn.Closing, turn.Interrupted = true, false, false
			turn.TerminalAt, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
			return turns
		}
		if record.Message.Model != "" {
			turn.Model = record.Message.Model
		}
		if text := strings.TrimSpace(transcriptText(record.Message.Content)); text != "" {
			if turn.Assistant == "" {
				turn.Assistant = text
			} else if turn.Assistant != text && !strings.Contains(turn.Assistant, text) {
				turn.Assistant += "\n" + text
			}
		}
		turn.Done = false
		turn.Closing = record.Message.StopReason == "end_turn"
		turn.Interrupted = claudeTerminalInterruption(record.Message.StopReason)
		if turn.Closing || turn.Interrupted {
			turn.TerminalAt, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
		}
	case "system":
		if record.Subtype == "turn_duration" && len(turns) > 0 {
			turn := &turns[len(turns)-1]
			turn.Closing = false
			turn.Done = true
			if turn.TerminalAt.IsZero() {
				turn.TerminalAt, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
			}
		}
	}
	return turns
}

// claudeRecordContinuesMessage reports whether a record is another content
// block of the API message messageID, which keeps a closing turn open.
func claudeRecordContinuesMessage(recordType, recordMessageID, messageID string) bool {
	return recordType == "assistant" && recordMessageID != "" && recordMessageID == messageID
}

func claudeTerminalInterruption(reason string) bool {
	switch reason {
	case "stop_sequence", "refusal", "max_tokens", "model_context_window_exceeded":
		return true
	default:
		return false
	}
}

func isClaudeInterruptMarker(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "[Request interrupted") || strings.HasPrefix(text, "[Request to interrupt")
}

func readClaudeCompactCompletion(path string, offset int64, requestUUID string) (bool, error) {
	matchedRequest := false
	completed := false
	err := findJSONLFrom(path, offset, func(line []byte) (bool, error) {
		var record claudeRecord
		if json.Unmarshal(line, &record) != nil {
			return false, nil
		}
		if record.Type == "user" && record.UUID == requestUUID && strings.TrimSpace(transcriptText(record.Message.Content)) == "/compact" {
			matchedRequest = true
			return false, nil
		}
		if matchedRequest && record.Type == "system" && record.Subtype == "compact_boundary" {
			completed = true
			return true, nil
		}
		if _, human := claudePromptText(record.Origin.Kind, transcriptText(record.Message.Content)); matchedRequest && record.Type == "user" && human {
			return false, fmt.Errorf("another prompt arrived before the compact boundary")
		}
		return false, nil
	})
	return completed, err
}

func transcriptText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			entry, ok := item.(map[string]any)
			if !ok || entry["type"] != "text" {
				continue
			}
			if text, _ := entry["text"].(string); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func isHumanTranscriptText(text string) bool {
	return IsTypedHumanPrompt(text)
}

// claudeForkedFrom returns the conversation a transcript was branched from.
// Claude copies the original's lines into a branch and marks each copy with
// forkedFrom, so the first record carries it.
func claudeForkedFrom(path string) string {
	var record struct {
		ForkedFrom struct {
			SessionID string `json:"sessionId"`
		} `json:"forkedFrom"`
	}
	if _, err := readFirstJSONL(path, &record); err != nil {
		return ""
	}
	return record.ForkedFrom.SessionID
}

// claudeTurnError is the error a turn the API refused ends with.
func claudeTurnError(turn claudeTurn) string {
	if turn.APIError == "" && !turn.UsageLimited {
		return ""
	}
	return claudeAPIErrorText(turn.UsageLimited, turn.APIError)
}

// claudeRecordAPIError is the error of a record Claude Code wrote in place of
// a reply the API refused, or empty for any other record.
func claudeRecordAPIError(record map[string]any) string {
	if apiError, _ := record["isApiErrorMessage"].(bool); !apiError || str(record, "type") != "assistant" {
		return ""
	}
	message, _ := record["message"].(map[string]any)
	return claudeAPIErrorText(claudeUsageLimitRecord(record), strings.TrimSpace(claudeMessageText(message["content"])))
}

func claudeAPIErrorText(usageLimited bool, message string) string {
	if usageLimited {
		return "Claude is out of usage (" + message + ")"
	}
	return "Claude API error: " + message
}
