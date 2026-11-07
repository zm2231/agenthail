package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// IdleCompactionState continues a read of the same file when it has grown by
// at most the tail it would read afresh, reading only up to the size it has
// when opened, and otherwise reads that tail; a request older than the tail
// is not found, which leaves LastRequestAt zero.
func (c *Claude) IdleCompactionState(ctx context.Context, sess *surface.Session, from surface.IdleCompactionRead) (surface.IdleCompactionRead, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	file, err := os.Open(path)
	if err != nil {
		return surface.IdleCompactionRead{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return surface.IdleCompactionRead{}, err
	}
	identity := transcriptFileIdentity(info)
	if identity != "" && from.File == identity && from.Offset > 0 && from.Offset <= info.Size() && info.Size()-from.Offset <= claudeIdleCompactionTailBytes {
		appended := make([]byte, info.Size()-from.Offset)
		if _, err := file.ReadAt(appended, from.Offset); err != nil && err != io.EOF {
			return surface.IdleCompactionRead{}, err
		}
		read := from
		complete := bytes.LastIndexByte(appended, '\n') + 1
		for line := range bytes.SplitSeq(appended[:complete], []byte("\n")) {
			if len(line) > 0 {
				observeIdleRecord(&read.State, line)
			}
		}
		read.Offset += int64(complete)
		return read, ctx.Err()
	}
	lines, end, err := readRecentJSONLLines(ctx, path, 0, claudeIdleCompactionTailBytes, maxClaudeTranscriptRecordBytes)
	if err != nil {
		return surface.IdleCompactionRead{}, err
	}
	read := surface.IdleCompactionRead{Offset: end, File: identity}
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
