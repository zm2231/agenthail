package surfaces

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeCompletionCursor struct {
	Offset      int64
	Anchor      string
	Current     claudeTurn
	CompletedID string
}

func (c *Claude) ReadCompletions(ctx context.Context, sess *surface.Session, cursor string) (surface.CompletionBatch, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	var checkpoint claudeCompletionCursor
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &checkpoint); err != nil {
			return surface.CompletionBatch{}, err
		}
		anchor, err := completionAnchor(path, checkpoint.Offset)
		if err != nil {
			return surface.CompletionBatch{}, err
		}
		if anchor != checkpoint.Anchor {
			return surface.CompletionBatch{}, fmt.Errorf("relay transcript changed before its saved cursor; restore the transcript or recreate the relay")
		}
	}
	state := &claudeObservationState{current: checkpoint.Current, hasCurrent: checkpoint.Current.UserID != "" || checkpoint.Current.MessageID != ""}
	batch := surface.CompletionBatch{}
	apply := func(line []byte) error {
		var record claudeRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("invalid relay transcript record: %w", err)
		}
		if err := state.apply(line); err != nil {
			return err
		}
		if state.hasCompleted && state.completed.MessageID != checkpoint.CompletedID {
			turn := state.completed
			checkpoint.CompletedID = turn.MessageID
			if cursor != "" && !turn.Interrupted {
				batch.Events = append(batch.Events, surface.Completion{ID: turn.MessageID, InputID: turn.UserID, Text: turn.Assistant, User: turn.User, CompletedAt: turn.TerminalAt})
			}
		}
		return nil
	}
	// Baseline once from the bounded tail; subsequent reads resume the durable
	// byte offset even across daemon restarts and retain every intervening turn.
	if cursor == "" {
		lines, offset, err := readRecentJSONLLines(ctx, path, 0, initialClaudeObservationBytes, maxClaudeTranscriptRecordBytes)
		if err != nil {
			return batch, err
		}
		for _, line := range lines {
			if err := apply(line); err != nil {
				return batch, err
			}
		}
		checkpoint.Offset = offset
	} else {
		offset, err := scanAppendedJSONL(ctx, path, checkpoint.Offset, maxClaudeTranscriptRecordBytes, apply)
		if err != nil {
			return surface.CompletionBatch{}, err
		}
		checkpoint.Offset = offset
	}
	checkpoint.Current = state.current
	anchor, err := completionAnchor(path, checkpoint.Offset)
	if err != nil {
		return surface.CompletionBatch{}, err
	}
	checkpoint.Anchor = anchor
	encoded, err := json.Marshal(checkpoint)
	batch.Cursor = string(encoded)
	return batch, err
}

func completionAnchor(path string, offset int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if offset < 0 || offset > info.Size() {
		return "", fmt.Errorf("relay transcript was truncated before its saved cursor")
	}
	start := max(int64(0), offset-256)
	data := make([]byte, offset-start)
	if _, err := io.ReadFull(io.NewSectionReader(f, start, offset-start), data); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
