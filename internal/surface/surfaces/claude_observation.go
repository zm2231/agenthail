package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

const initialClaudeObservationBytes = 32 * 1024 * 1024

type claudeObservationState struct {
	offset               int64
	fileInfo             os.FileInfo
	current              claudeTurn
	completed            claudeTurn
	hasCurrent           bool
	hasCompleted         bool
	latestCommandAt      time.Time
	latestCompletedAt    time.Time
	pendingWithoutTime   int
	markersWithoutTime   int
	completedWithoutTime int
}

func (c *Claude) observeTranscript(ctx context.Context, path string) (*claudeObservationState, error) {
	c.observeMu.Lock()
	defer c.observeMu.Unlock()
	if c.observeState == nil {
		c.observeState = map[string]*claudeObservationState{}
	}
	state := c.observeState[path]
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if state == nil || info.Size() < state.offset || (state.fileInfo != nil && !os.SameFile(state.fileInfo, info)) {
		state = &claudeObservationState{}
		lines, offset, err := readRecentJSONLLines(ctx, path, 0, initialClaudeObservationBytes, maxClaudeTranscriptRecordBytes)
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			_ = state.apply(line)
		}
		state.offset = offset
		state.fileInfo = info
		c.observeState[path] = state
	}
	offset, err := scanAppendedJSONL(ctx, path, state.offset, maxClaudeTranscriptRecordBytes, state.apply)
	state.offset = offset
	state.fileInfo = info
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (s *claudeObservationState) apply(line []byte) error {
	var record claudeRecord
	if json.Unmarshal(line, &record) != nil {
		return nil
	}
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	s.applyCompact(record, at)
	s.applyTurn(record, at)
	return nil
}

func (s *claudeObservationState) applyTurn(record claudeRecord, at time.Time) {
	switch record.Type {
	case "user":
		text := transcriptText(record.Message.Content)
		if isClaudeInterruptMarker(text) {
			if s.hasCurrent {
				s.current.Interrupted = true
			}
			return
		}
		if !isHumanTranscriptText(text) || record.Message.ToolUseID != "" {
			return
		}
		s.current = claudeTurn{UserID: record.UUID, User: strings.TrimSpace(text)}
		s.hasCurrent = true
	case "assistant":
		if !s.hasCurrent {
			s.hasCurrent = true
		}
		if s.current.MessageID != "" && record.Message.ID != "" && s.current.MessageID != record.Message.ID && s.current.Done {
			s.current = claudeTurn{}
		}
		if record.Message.ID != "" {
			s.current.MessageID = record.Message.ID
		} else if s.current.MessageID == "" {
			s.current.MessageID = record.UUID
		}
		if record.Message.Model != "" {
			s.current.Model = record.Message.Model
		}
		if text := strings.TrimSpace(transcriptText(record.Message.Content)); text != "" {
			if s.current.Assistant == "" {
				s.current.Assistant = text
			} else if s.current.Assistant != text && !strings.Contains(s.current.Assistant, text) {
				s.current.Assistant += "\n" + text
			}
		}
		s.current.Done = record.Message.StopReason == "end_turn"
		s.current.Interrupted = claudeTerminalInterruption(record.Message.StopReason)
		if s.current.Done || s.current.Interrupted {
			s.current.TerminalAt = at
		}
		if s.current.Done && s.current.MessageID != "" {
			s.completed = s.current
			s.hasCompleted = true
		}
	case "system":
		if record.Subtype == "turn_duration" && s.hasCurrent {
			s.current.Done = true
			s.current.TerminalAt = at
			if s.current.MessageID != "" {
				s.completed = s.current
				s.hasCompleted = true
			}
		}
	}
}

func (s *claudeObservationState) applyCompact(record claudeRecord, at time.Time) {
	if record.Type == "assistant" && (record.Message.StopReason == "end_turn" || claudeTerminalInterruption(record.Message.StopReason)) && (s.latestCompletedAt.IsZero() || at.After(s.latestCompletedAt)) {
		s.latestCompletedAt = at
	}
	if record.Type == "user" {
		content := strings.TrimSpace(transcriptText(record.Message.Content))
		if at.IsZero() {
			switch {
			case content == "/compact":
				s.pendingWithoutTime++
			case strings.Contains(content, "<command-name>/compact</command-name>"):
				s.markersWithoutTime++
			case s.markersWithoutTime > 0 && strings.HasPrefix(content, "<local-command-stdout>"):
				s.markersWithoutTime--
				if s.completedWithoutTime > 0 {
					s.completedWithoutTime--
				} else if s.pendingWithoutTime > 0 {
					s.pendingWithoutTime--
				}
			}
			return
		}
		switch {
		case content == "/compact", strings.Contains(content, "<command-name>/compact</command-name>"):
			if s.latestCommandAt.IsZero() || at.After(s.latestCommandAt) {
				s.latestCommandAt = at
			}
		case strings.HasPrefix(content, "<local-command-stdout>"):
			if s.latestCompletedAt.IsZero() || at.After(s.latestCompletedAt) {
				s.latestCompletedAt = at
			}
		}
		return
	}
	if record.Type != "system" || (record.Subtype != "compact_boundary" && record.Subtype != "local_command" && record.Subtype != "turn_duration") {
		return
	}
	if at.IsZero() {
		if record.Subtype == "compact_boundary" && s.pendingWithoutTime > 0 {
			s.pendingWithoutTime--
			s.completedWithoutTime++
		} else if record.Subtype == "local_command" && s.markersWithoutTime > 0 {
			s.markersWithoutTime--
			if s.completedWithoutTime > 0 {
				s.completedWithoutTime--
			} else if s.pendingWithoutTime > 0 {
				s.pendingWithoutTime--
			}
		}
		return
	}
	if s.latestCompletedAt.IsZero() || at.After(s.latestCompletedAt) {
		s.latestCompletedAt = at
	}
}

func (s *claudeObservationState) compactPending() bool {
	return (!s.latestCommandAt.IsZero() && (s.latestCompletedAt.IsZero() || s.latestCommandAt.After(s.latestCompletedAt))) || s.pendingWithoutTime > 0
}
