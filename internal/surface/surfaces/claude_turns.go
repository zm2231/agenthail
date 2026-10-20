package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeTurnEnd struct {
	TurnID string
	Key    string
	Status string
	Turn   *surface.TurnMetadata
}

// claudeTurnTracker derives, one transcript record at a time, the turn each
// record belongs to and where turns end. The live stream and transcript pages
// both use it, so a turn read from history carries the same turn ID and done
// key as the same turn streamed live.
type claudeTurnTracker struct {
	current  string
	closing  surface.ClaudeClosingTurn
	terminal map[string]bool
	meter    claudeTurnMeter
}

// claudeTurnMeter accumulates the turn whose start record the tracker saw.
// Claude writes a message's usage on each of its records, partial first, so
// the last record of each message counts.
type claudeTurnMeter struct {
	turnID    string
	startedAt time.Time
	lastAt    time.Time
	model     string
	usage     map[string]surface.TurnUsage
}

func newClaudeTurnTracker(current string, closing surface.ClaudeClosingTurn) *claudeTurnTracker {
	return &claudeTurnTracker{current: current, closing: closing, terminal: map[string]bool{}}
}

// observe returns the record's turn, the turns that ended at it, and whether
// it interrupted its turn. A done key names the record that ended the turn
// and the item index after its items, as the live stream has always keyed it.
func (t *claudeTurnTracker) observe(record map[string]any, offset int64, line []byte, itemCount int) (string, []claudeTurnEnd, bool) {
	var ended []claudeTurnEnd
	messageID := strNested(record, "message", "id")
	if t.closing.TurnID != "" && !claudeRecordContinuesMessage(str(record, "type"), messageID, t.closing.MessageID) {
		if !claudeRecordIsInterrupt(record) {
			ended = t.finish(ended, t.closing.TurnID, t.closing.Key, "")
		}
		t.closing = surface.ClaudeClosingTurn{}
	}
	if claudeRecordStartsTurn(record) {
		t.current = str(record, "uuid")
		t.meter = claudeTurnMeter{turnID: t.current, usage: map[string]surface.TurnUsage{}}
	}
	turnID := t.current
	if turnID != "" && turnID == t.meter.turnID {
		t.meter.observe(record)
	}
	done, ending, interrupted := claudeStreamTerminal(record)
	if interrupted {
		if turnID != "" {
			ended = t.finish(ended, turnID, stableTimelineItemID(offset, line, 0), "cancelled")
		}
		return turnID, ended, true
	}
	if turnID == "" {
		return turnID, ended, false
	}
	key := stableTimelineItemID(offset, line, itemCount)
	if ending {
		at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
		t.closing = surface.ClaudeClosingTurn{TurnID: turnID, MessageID: messageID, Key: key, At: at}
	} else if done {
		ended = t.finish(ended, turnID, key, "")
	}
	return turnID, ended, false
}

// settle ends a turn still closing once its session has gone idle after the
// turn's last block, since nothing else will follow it.
func (t *claudeTurnTracker) settle(settled func(time.Time) bool) []claudeTurnEnd {
	if t.closing.TurnID == "" || settled == nil || !settled(t.closing.At) {
		return nil
	}
	ended := t.finish(nil, t.closing.TurnID, t.closing.Key, "")
	t.closing = surface.ClaudeClosingTurn{}
	return ended
}

func (t *claudeTurnTracker) finish(ended []claudeTurnEnd, turnID, key, status string) []claudeTurnEnd {
	if t.terminal[turnID] {
		return ended
	}
	t.terminal[turnID] = true
	turn := surface.NewTurnMetadata(surface.TurnStateOfDone(status), "", nil, nil)
	if turnID == t.meter.turnID {
		turn = t.meter.metadata(status)
	}
	return append(ended, claudeTurnEnd{TurnID: turnID, Key: key, Status: status, Turn: turn})
}

func (m *claudeTurnMeter) observe(record map[string]any) {
	if at, err := time.Parse(time.RFC3339Nano, str(record, "timestamp")); err == nil {
		if m.startedAt.IsZero() {
			m.startedAt = at
		}
		m.lastAt = at
	}
	if str(record, "type") != "assistant" {
		return
	}
	message, _ := record["message"].(map[string]any)
	if model := str(message, "model"); model != "" && model != "<synthetic>" {
		m.model = model
	}
	usage, _ := message["usage"].(map[string]any)
	id := str(message, "id")
	if usage == nil || id == "" {
		return
	}
	cacheRead := int64Value(usage["cache_read_input_tokens"])
	cacheWrite := int64Value(usage["cache_creation_input_tokens"])
	counted := surface.TurnUsage{
		InputTokens:       int64Value(usage["input_tokens"]) + cacheRead + cacheWrite,
		CachedInputTokens: cacheRead,
		CacheWriteTokens:  cacheWrite,
		OutputTokens:      int64Value(usage["output_tokens"]),
	}
	details, _ := usage["output_tokens_details"].(map[string]any)
	if thinking, ok := details["thinking_tokens"]; ok {
		reasoning := int64Value(thinking)
		counted.ReasoningTokens = &reasoning
	}
	m.usage[id] = counted
}

func (m *claudeTurnMeter) metadata(status string) *surface.TurnMetadata {
	var duration *int64
	if !m.startedAt.IsZero() {
		ms := m.lastAt.Sub(m.startedAt).Milliseconds()
		duration = &ms
	}
	var usage *surface.TurnUsage
	if len(m.usage) > 0 {
		usage = &surface.TurnUsage{}
		reasoningKnown := true
		var reasoning int64
		for _, counted := range m.usage {
			usage.InputTokens += counted.InputTokens
			usage.CachedInputTokens += counted.CachedInputTokens
			usage.CacheWriteTokens += counted.CacheWriteTokens
			usage.OutputTokens += counted.OutputTokens
			if counted.ReasoningTokens == nil {
				reasoningKnown = false
			} else {
				reasoning += *counted.ReasoningTokens
			}
		}
		if reasoningKnown {
			usage.ReasoningTokens = &reasoning
		}
	}
	return surface.NewTurnMetadata(surface.TurnStateOfDone(status), m.model, duration, usage)
}

// turnScanLimit bounds how far back a reader looks for the record that
// started the turn in progress at an offset.
var turnScanLimit int64 = 64 << 20

// claudeTurnStateAt returns the tracker as it stands just before offset: the
// turn in progress there and any turn still closing. It replays from the
// record that started that turn, at most turnScanLimit bytes back; a
// turn that started further back is unknown and leaves the tracker empty.
func claudeTurnStateAt(ctx context.Context, file *os.File, offset int64) (*claudeTurnTracker, error) {
	tracker := newClaudeTurnTracker("", surface.ClaudeClosingTurn{})
	from, found, err := transcriptRecordBefore(ctx, file, offset, []byte(`"user"`), claudeRecordStartsTurn)
	if err != nil || !found {
		return tracker, err
	}
	err = scanTranscriptRecords(ctx, file, from, offset, func(record map[string]any, recordOffset int64, line []byte) {
		tracker.observe(record, recordOffset, line, len(claudeTimelineItems(record)))
	})
	return tracker, err
}

// transcriptRecordBefore returns the offset of the last record before offset
// that match accepts, looking only at lines containing needle.
func transcriptRecordBefore(ctx context.Context, file *os.File, offset int64, needle []byte, match func(map[string]any) bool) (int64, bool, error) {
	const chunk = 1 << 20
	floor := max(0, offset-turnScanLimit)
	end := offset
	var carry []byte
	for end > floor {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		start := max(floor, end-chunk)
		buf := make([]byte, end-start, end-start+int64(len(carry)))
		if _, err := file.ReadAt(buf, start); err != nil && err != io.EOF {
			return 0, false, err
		}
		buf = append(buf, carry...)
		next := len(buf)
		for next > 0 {
			previous := bytes.LastIndexByte(buf[:next-1], '\n')
			if previous < 0 && start > 0 {
				break
			}
			line := buf[previous+1 : next]
			if bytes.Contains(line, needle) {
				var record map[string]any
				if json.Unmarshal(line, &record) == nil && match(record) {
					return start + int64(previous+1), true, nil
				}
			}
			next = previous + 1
		}
		carry = append([]byte(nil), buf[:next]...)
		end = start
	}
	return 0, false, nil
}

func scanTranscriptRecords(ctx context.Context, file *os.File, from, to int64, visit func(record map[string]any, offset int64, line []byte)) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, from, to-from), 1<<20)
	offset := from
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			var record map[string]any
			if json.Unmarshal(line, &record) == nil {
				visit(record, offset, line)
			}
		}
		offset += int64(len(line))
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// TranscriptItems returns, oldest first and with their full text, the timeline
// items the transcript records from the offset to its last complete record,
// as a transcript page derives them. A prompt recorded twice, queued and then
// delivered, appears once, at its delivery.
func (c *Claude) TranscriptItems(ctx context.Context, session *surface.Session, from int64) ([]surface.TimelineItem, error) {
	path, err := c.sessionTranscript(session)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	end, err := completeJSONLEnd(ctx, file, info.Size())
	if err != nil {
		return nil, err
	}
	from = min(max(0, from), end)
	turns, err := claudeTurnStateAt(ctx, file, from)
	if err != nil {
		return nil, err
	}
	var items []surface.TimelineItem
	var itemErr error
	err = scanTranscriptRecords(ctx, file, from, end, func(record map[string]any, offset int64, line []byte) {
		if itemErr != nil {
			return
		}
		turnID, ended, interrupted := turns.observe(record, offset, line, len(claudeTimelineItems(record)))
		recordItems, err := claudeRecordTimelineItems(ctx, record, offset, line, turnID, ended, interrupted)
		if err != nil {
			itemErr = err
			return
		}
		items = append(items, recordItems...)
	})
	if err == nil {
		err = itemErr
	}
	if err != nil {
		return nil, err
	}
	if ended := turns.settle(func(at time.Time) bool { return c.settledAfter(session, at) }); len(ended) > 0 {
		at := ""
		if len(items) > 0 {
			at = items[len(items)-1].Timestamp
		}
		for _, end := range ended {
			items = append(items, surface.TimelineItem{ID: end.Key, Kind: "done", TurnID: end.TurnID, Status: end.Status, Timestamp: at, Turn: end.Turn})
		}
	}
	seen := map[string]bool{}
	kept := make([]surface.TimelineItem, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		if !seen[items[i].ID] {
			seen[items[i].ID] = true
			kept = append(kept, items[i])
		}
	}
	slices.Reverse(kept)
	return kept, nil
}
