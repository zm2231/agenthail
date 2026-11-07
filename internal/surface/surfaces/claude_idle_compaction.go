package surfaces

import (
	"context"
	"encoding/json"
	"os"
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

// IdleCompactionState continues from a read when the transcript has grown by
// at most the tail it would read afresh, and otherwise reads that tail; a
// request older than the tail is not found, which leaves LastRequestAt zero.
func (c *Claude) IdleCompactionState(ctx context.Context, sess *surface.Session, from surface.IdleCompactionRead) (surface.IdleCompactionRead, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	info, err := os.Stat(path)
	if err != nil {
		return surface.IdleCompactionRead{}, err
	}
	if from.Offset > 0 && from.Offset <= info.Size() && info.Size()-from.Offset <= claudeIdleCompactionTailBytes {
		read := from
		offset, err := scanAppendedJSONL(ctx, path, from.Offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
			observeIdleRecord(&read.State, line)
			return nil
		})
		if err != nil {
			return surface.IdleCompactionRead{}, err
		}
		read.Offset = offset
		return read, nil
	}
	lines, end, err := readRecentJSONLLines(ctx, path, 0, claudeIdleCompactionTailBytes, maxClaudeTranscriptRecordBytes)
	if err != nil {
		return surface.IdleCompactionRead{}, err
	}
	read := surface.IdleCompactionRead{Offset: end}
	for _, line := range lines {
		observeIdleRecord(&read.State, line)
	}
	return read, nil
}

func observeIdleRecord(state *surface.IdleCompactionState, line []byte) {
	var record claudeIdleRecord
	if json.Unmarshal(line, &record) != nil || record.IsSidechain {
		return
	}
	switch {
	case record.Type == "assistant" && record.Message.Model != "" && record.Message.Model != claudeSyntheticModel:
		at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		if err != nil {
			return
		}
		usage := record.Message.Usage
		state.LastRequestAt = at
		state.ContextTokens = usage.InputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
		state.CompactedSince = false
		// A cache read refreshes the entry it reads for that entry's own
		// TTL, so the cache in effect is the one the last write chose.
		if usage.CacheCreationTokens > 0 {
			state.OneHourCache = usage.CacheCreation.OneHour > 0
		}
	case record.Type == "system" && record.Subtype == "compact_boundary" && !state.LastRequestAt.IsZero():
		state.CompactedSince = true
	}
}
