package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// initialClaudeObservationBytes bounds the transcript tail read when a Claude
// session is first observed. Tests shrink it to exercise overlap cheaply.
var initialClaudeObservationBytes int64 = 32 * 1024 * 1024

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
	tasks                claudePlanState
}

// observeTranscript advances the shared observation of path and returns a copy,
// so concurrent observers and streams read it without holding observeMu.
func (c *Claude) observeTranscript(ctx context.Context, path string, sess *surface.Session) (*claudeObservationState, error) {
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
	state.tasks.resolve(path)
	if state.hasCurrent && state.current.Closing && c.settledAfter(sess, state.current.TerminalAt) {
		state.settle()
	}
	snapshot := *state
	return &snapshot, nil
}

func (s *claudeObservationState) apply(line []byte) error {
	var record claudeRecord
	if json.Unmarshal(line, &record) != nil {
		return nil
	}
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	s.applyCompact(record, at)
	s.applyTurn(record)
	s.tasks.apply(record, line)
	return nil
}

// applyTurn advances the current turn and records it as completed when it
// closes, so observation and completion capture share one close rule.
func (s *claudeObservationState) applyTurn(record claudeRecord) {
	var turns []claudeTurn
	if s.hasCurrent {
		turns = []claudeTurn{s.current}
	}
	wasDone := s.hasCurrent && s.current.Done
	turns = appendClaudeTurn(turns, record)
	if len(turns) == 0 {
		return
	}
	if s.hasCurrent && !wasDone && turns[0].Done && turns[0].MessageID != "" {
		s.completed = turns[0]
		s.hasCompleted = true
	}
	s.current = turns[len(turns)-1]
	s.hasCurrent = true
}

// settle completes a closing turn whose process has since gone idle.
func (s *claudeObservationState) settle() {
	s.current.Closing = false
	s.current.Done = true
	if s.current.MessageID != "" {
		s.completed = s.current
		s.hasCompleted = true
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
