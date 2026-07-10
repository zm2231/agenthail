package surfaces

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const maxClaudeTranscriptRecordBytes = 32 * 1024 * 1024

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

type claudeTurn struct {
	UserID      string
	MessageID   string
	User        string
	Assistant   string
	Model       string
	Done        bool
	Interrupted bool
}

type claudeRecord struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	Subtype string `json:"subtype"`
	Content string `json:"content"`
	Message struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    any    `json:"content"`
		ToolUseID  string `json:"tool_use_id"`
	} `json:"message"`
}

func readClaudeTurns(path string) ([]claudeTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var turns []claudeTurn
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxClaudeTranscriptRecordBytes)
	for scanner.Scan() {
		var record claudeRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		switch record.Type {
		case "user":
			text := transcriptText(record.Message.Content)
			if isClaudeInterruptMarker(text) {
				if len(turns) > 0 {
					turns[len(turns)-1].Interrupted = true
				}
				continue
			}
			if !isHumanTranscriptText(text) || record.Message.ToolUseID != "" {
				continue
			}
			turns = append(turns, claudeTurn{UserID: record.UUID, User: strings.TrimSpace(text)})
		case "assistant":
			if len(turns) == 0 {
				turns = append(turns, claudeTurn{})
			}
			turn := &turns[len(turns)-1]
			if turn.MessageID != "" && record.Message.ID != "" && turn.MessageID != record.Message.ID && turn.Done {
				turns = append(turns, claudeTurn{})
				turn = &turns[len(turns)-1]
			}
			if record.Message.ID != "" {
				turn.MessageID = record.Message.ID
			} else if turn.MessageID == "" {
				turn.MessageID = record.UUID
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
			turn.Done = record.Message.StopReason == "end_turn"
			turn.Interrupted = record.Message.StopReason == "stop_sequence" || record.Message.StopReason == "refusal"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan Claude transcript: %w", err)
	}
	return turns, nil
}

func isClaudeInterruptMarker(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "[Request interrupted") || strings.HasPrefix(text, "[Request to interrupt")
}

func readClaudeCommandResult(path string, offset int64, commandName, commandArgs string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", false, err
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxClaudeTranscriptRecordBytes)
	matchedCommand := false
	for scanner.Scan() {
		var record claudeRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			continue
		}
		if record.Type == "user" {
			content := transcriptText(record.Message.Content)
			matchedCommand = strings.Contains(content, "<command-name>"+commandName+"</command-name>") && strings.Contains(content, "<command-args>"+commandArgs+"</command-args>")
			continue
		}
		if !matchedCommand || record.Type != "system" || record.Subtype != "local_command" || record.Content == "" {
			continue
		}
		result := strings.TrimSpace(record.Content)
		result = strings.TrimPrefix(result, "<local-command-stdout>")
		result = strings.TrimSuffix(result, "</local-command-stdout>")
		return strings.TrimSpace(ansiEscapeRe.ReplaceAllString(result, "")), true, nil
	}
	if err := scanner.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
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
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	// Claude writes slash commands twice: once as the literal command and once
	// as tagged local-command metadata. Neither entry represents an agent turn.
	if text == "/compact" || text == "/model" || strings.HasPrefix(text, "/model ") {
		return false
	}
	for _, prefix := range []string{
		"<local-command", "<command-", "<system", "[Request interrupted", "[Request to",
		"This session is being continued from a previous conversation that ran out of context.",
	} {
		if strings.HasPrefix(text, prefix) {
			return false
		}
	}
	return true
}
