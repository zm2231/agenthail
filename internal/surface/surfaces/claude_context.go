package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeContextState struct {
	offset              int64
	usage               surface.ContextUsage
	latestCommandAt     time.Time
	latestCommandDoneAt time.Time
	seenBoundaries      map[string]bool
}

type claudeContextRecord struct {
	UUID            string `json:"uuid"`
	Type            string `json:"type"`
	Subtype         string `json:"subtype"`
	Timestamp       string `json:"timestamp"`
	CompactMetadata struct {
		PreTokens  int64 `json:"preTokens"`
		PostTokens int64 `json:"postTokens"`
	} `json:"compactMetadata"`
	Message struct {
		Model   string `json:"model"`
		Content any    `json:"content"`
		Usage   struct {
			InputTokens         int64 `json:"input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (c *Claude) ContextUsage(ctx context.Context, sess *surface.Session) (*surface.ContextUsage, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	c.contextMu.Lock()
	defer c.contextMu.Unlock()
	if c.contextState == nil {
		c.contextState = map[string]*claudeContextState{}
	}
	state := c.contextState[path]
	if state == nil || info.Size() < state.offset {
		state = &claudeContextState{seenBoundaries: map[string]bool{}}
		c.contextState[path] = state
	}
	if state.seenBoundaries == nil {
		state.seenBoundaries = map[string]bool{}
	}
	offset, err := scanAppendedJSONL(ctx, path, state.offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		var record claudeContextRecord
		if json.Unmarshal(line, &record) != nil {
			return nil
		}
		at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
		switch {
		case record.Type == "assistant":
			used := record.Message.Usage.InputTokens + record.Message.Usage.CacheCreationTokens + record.Message.Usage.CacheReadTokens
			if used > 0 && contextEventIsCurrent(at, state.usage.UpdatedAt) {
				state.usage.UsedTokens = used
				state.usage.InputTokens = record.Message.Usage.InputTokens
				state.usage.CachedInputTokens = record.Message.Usage.CacheCreationTokens + record.Message.Usage.CacheReadTokens
				state.usage.OutputTokens = record.Message.Usage.OutputTokens
				state.usage.ContextWindow = claudeContextWindow(record.Message.Model)
				state.usage.WindowEstimated = true
				state.usage.UpdatedAt = at
			}
		case record.Type == "user":
			content := strings.TrimSpace(transcriptText(record.Message.Content))
			switch {
			case content == "/compact" && compactCommandIsNewer(at, state.usage.LastCompactedAt):
				state.rememberCommandTime(at)
			case strings.Contains(content, "<command-name>/compact</command-name>") && compactCommandIsNewer(at, state.usage.LastCompactedAt):
				state.rememberCommandTime(at)
			case strings.HasPrefix(content, "<local-command-stdout>"):
				state.rememberCommandCompletion(at)
			}
		case record.Type == "system" && record.Subtype == "compact_boundary":
			key := compactBoundaryKey(record)
			if state.seenBoundaries[key] {
				return nil
			}
			state.seenBoundaries[key] = true
			state.usage.CompactionCount++
			if contextEventIsCurrent(at, state.usage.LastCompactedAt) {
				state.usage.PreCompactTokens = record.CompactMetadata.PreTokens
				state.usage.PostCompactTokens = record.CompactMetadata.PostTokens
				state.usage.ReclaimedTokens = 0
				if record.CompactMetadata.PreTokens > record.CompactMetadata.PostTokens {
					state.usage.ReclaimedTokens = record.CompactMetadata.PreTokens - record.CompactMetadata.PostTokens
				}
				state.usage.LastCompactedAt = at
				if contextEventIsCurrent(at, state.usage.UpdatedAt) {
					state.usage.UsedTokens = record.CompactMetadata.PostTokens
					state.usage.InputTokens = 0
					state.usage.CachedInputTokens = 0
					state.usage.OutputTokens = 0
					state.usage.UpdatedAt = at
				}
			}
		case record.Type == "system" && record.Subtype == "local_command":
			state.rememberCommandCompletion(at)
		}
		return nil
	})
	state.offset = offset
	if err != nil {
		return nil, err
	}
	completedAt := state.usage.LastCompactedAt
	if state.latestCommandDoneAt.After(completedAt) {
		completedAt = state.latestCommandDoneAt
	}
	state.usage.Compacting = !state.latestCommandAt.IsZero() && state.latestCommandAt.After(completedAt)
	if state.usage.ContextWindow == 0 && state.usage.CompactionCount == 0 {
		return nil, nil
	}
	usage := state.usage
	return &usage, nil
}

func compactBoundaryKey(record claudeContextRecord) string {
	if record.UUID != "" {
		return record.UUID
	}
	return record.Timestamp + ":" + strconv.FormatInt(record.CompactMetadata.PreTokens, 10) + ":" + strconv.FormatInt(record.CompactMetadata.PostTokens, 10)
}

func contextEventIsCurrent(candidate, current time.Time) bool {
	return current.IsZero() || (!candidate.IsZero() && !candidate.Before(current))
}

func (s *claudeContextState) rememberCommandTime(at time.Time) {
	if contextEventIsCurrent(at, s.latestCommandAt) {
		s.latestCommandAt = at
	}
}

func (s *claudeContextState) rememberCommandCompletion(at time.Time) {
	if contextEventIsCurrent(at, s.latestCommandDoneAt) {
		s.latestCommandDoneAt = at
	}
}

func compactCommandIsNewer(commandAt, boundaryAt time.Time) bool {
	return boundaryAt.IsZero() || (!commandAt.IsZero() && commandAt.After(boundaryAt))
}

func claudeContextWindow(model string) int64 {
	if strings.Contains(strings.ToLower(model), "[1m]") {
		return 1_000_000
	}
	return 200_000
}
