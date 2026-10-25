package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	if sess.Transport == "uds" && uuid != "" {
		return surface.ErrUnsupported
	}
	return c.streamTimeline(ctx, sess, uuid, onEvent, timeout)
}

func (c *Claude) streamTimeline(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	var commands *commandsWatch
	if uuid == "" {
		onEvent = serializedEvents(onEvent)
		listed := *sess
		commands = watchCommands(ctx, func(ctx context.Context) (*surface.RuntimeCommands, error) { return c.Commands(ctx, &listed) }, onEvent)
		defer commands.Stop()
	}
	path, err := c.sessionTranscript(sess)
	if err != nil {
		return err
	}
	fromStart := sess.TranscriptFromStart
	if path == "" || !fileExists(path) {
		if !claudeHosted(sess) {
			return fmt.Errorf("no local transcript for streaming")
		}
		if path, err = c.awaitHostTranscript(ctx, sess, timeout); err != nil {
			return err
		}
		fromStart = true
	}
	wake := filewake.Follow(path)
	defer wake.Release()
	state, err := c.observeTranscript(ctx, path, sess)
	if err != nil {
		return err
	}
	identity := state.cursor.identity
	offset := state.cursor.offset
	var closing surface.ClaudeClosingTurn
	if uuid != "" {
		// A targeted wait looks back so a turn submitted before the tail started is still found;
		// events outside that turn are filtered below.
		offset = max(int64(0), state.cursor.offset-initialClaudeObservationBytes)
	} else if sess.TranscriptOffsetSet && sess.TranscriptOffset <= state.cursor.offset && identity != "" && sess.TranscriptIdentity == identity {
		// The session source resumes at its seed or previous window boundary, so records written
		// in between are delivered once without replaying history the seed bounded away.
		offset = sess.TranscriptOffset
		closing = sess.ClaudeClosingTurn
	} else if !sess.TranscriptOffsetSet && fromStart {
		// The transcript was created after this stream or its source's seed looked for it, so
		// everything in it is new.
		offset = 0
	}
	if uuid != "" && offset > 0 {
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		if _, seekErr := file.Seek(offset, 0); seekErr != nil {
			file.Close()
			return seekErr
		}
		reader := bufio.NewReader(file)
		prefix, readErr := reader.ReadBytes('\n')
		file.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		offset += int64(len(prefix))
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	replayed, err := claudeTurnStateAt(ctx, file, offset)
	file.Close()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	turns := newClaudeTurnTracker(replayed.current, closing)
	turns.meter, turns.between, turns.betweenKnown = replayed.meter, replayed.between, replayed.betweenKnown
	completed := false
	finish := func(ended []claudeTurnEnd) {
		for _, end := range ended {
			if uuid != "" && end.TurnID != uuid && end.Status == "" {
				continue
			}
			onEvent(surface.StreamEvent{ID: end.Key, ProviderKey: "timeline:" + end.Key, Version: 1, Operation: "phase", TurnID: end.TurnID, Kind: "done", Status: end.Status, Text: end.Reason, Turn: end.Turn})
			if uuid != "" && end.TurnID == uuid && (end.Status == "" || end.Status == "failed") {
				completed = true
			}
		}
	}
	// readAppended reads what the transcript gained since offset, and its goal.
	var tail []byte
	readAppended := func() error {
		// A closing turn belongs to the bytes before offset; a rewritten transcript drops it.
		// The anchor then rolls forward over the bytes this scan parses, never a later read.
		var tailErr error
		tail, tailErr = transcriptTail(path, offset)
		if turns.closing.TurnID != "" && (tailErr != nil || anchorOf(tail) != turns.closing.Anchor) {
			turns.closing = surface.ClaudeClosingTurn{}
		}
		lineOffset := offset
		next, scanErr := scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			tail = append(tail, line...)
			if len(tail) > transcriptAnchorBytes {
				tail = append(tail[:0], tail[len(tail)-transcriptAnchorBytes:]...)
			}
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				return nil
			}
			items := claudeTimelineItems(record)
			turnID, ended, interrupted := turns.observe(record, recordOffset, line, len(items))
			if interrupted && uuid != "" && turnID == uuid {
				return fmt.Errorf("Claude turn %s was interrupted", uuid)
			}
			finish(ended)
			if interrupted {
				return nil
			}
			if err := decorateTimelineAttachments(ctx, items, record, "claude", recordOffset); err != nil {
				return err
			}
			if uuid != "" && turnID != uuid {
				return nil
			}
			for index, item := range items {
				key := item.ID
				if key == "" {
					key = stableTimelineItemID(recordOffset, line, index)
				}
				version := uint64(len(item.Text))
				if version == 0 {
					version = 1
				}
				at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
				onEvent(surface.StreamEvent{ID: key, ProviderKey: "timeline:" + key, Version: version, Operation: "upsert", Final: true, TurnID: turnID, MessageID: item.MessageID, Role: item.Role, Origin: item.Origin, Sender: item.Sender, Title: item.Title, CallID: item.CallID, Status: item.Status, ResetsAt: item.ResetsAt, Attachment: item.Attachment, Files: item.Files, Truncated: item.Truncated, TruncationReason: item.TruncationReason, Timestamp: at, Kind: item.Kind, Text: item.Text})
			}
			return nil
		})
		if scanErr != nil {
			return scanErr
		}
		offset = next
		if uuid == "" {
			goal, version, changed, goalErr := c.streamGoal(ctx, path)
			if goalErr != nil {
				return goalErr
			}
			if changed {
				onEvent(surface.StreamEvent{ID: "goal", ProviderKey: "goal", Version: version, Operation: "replace", Kind: "goal", Goal: goal})
			}
			commands.Refresh()
		}
		return nil
	}
	// read is the transcript as the last read found it. A pass that finds it
	// the same size, time and file has nothing to read and only settles turns.
	var read os.FileInfo
	for first := true; first || time.Now().Before(deadline); first = false {
		info, statErr := os.Stat(path)
		if statErr == nil {
			if current := transcriptFileIdentity(info); identity != "" && current != identity {
				// A transcript replaced at the same path is read from its start, as a shorter rewrite is.
				identity, offset = current, 0
			}
		}
		if statErr != nil || read == nil || !sameTranscriptState(read, info) {
			if err := readAppended(); err != nil {
				return err
			}
			read = info
		}
		finish(turns.settle(func(at time.Time) bool { return c.settledAfter(sess, at) }))
		if turns.closing.TurnID != "" {
			turns.closing.Anchor = anchorOf(tail)
		}
		if uuid == "" {
			sess.TranscriptOffset = offset
			sess.TranscriptOffsetSet = true
			sess.TranscriptIdentity = identity
			sess.ClaudeClosingTurn = turns.closing
		}
		if completed {
			return nil
		}
		if err := awaitTranscript(ctx, wake); err != nil {
			return err
		}
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
}
