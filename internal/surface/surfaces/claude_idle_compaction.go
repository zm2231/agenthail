package surfaces

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const claudeIdleCompactionTailBytes int64 = 4 << 20

type claudeIdleRecord struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens         int64 `json:"input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreation       struct {
				OneHour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// IdleCompactionState reads the end of the transcript; a request older than
// that end is not found, which leaves LastRequestAt zero.
func (c *Claude) IdleCompactionState(ctx context.Context, sess *surface.Session) (surface.IdleCompactionState, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	lines, _, err := readRecentJSONLLines(ctx, path, 0, claudeIdleCompactionTailBytes, maxClaudeTranscriptRecordBytes)
	if err != nil {
		return surface.IdleCompactionState{}, err
	}
	var state surface.IdleCompactionState
	for _, line := range lines {
		var record claudeIdleRecord
		if json.Unmarshal(line, &record) != nil || record.IsSidechain {
			continue
		}
		switch {
		case record.Type == "assistant" && record.Message.Model != "" && record.Message.Model != claudeSyntheticModel:
			at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
			if err != nil {
				continue
			}
			usage := record.Message.Usage
			state.LastRequestAt = at
			state.ContextTokens = usage.InputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
			state.CompactedSince = false
			if usage.CacheCreationTokens > 0 {
				state.OneHourCache = usage.CacheCreation.OneHour > 0
			}
		case record.Type == "system" && record.Subtype == "compact_boundary" && !state.LastRequestAt.IsZero():
			state.CompactedSince = true
		}
	}
	return state, nil
}
