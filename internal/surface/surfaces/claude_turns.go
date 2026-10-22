package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeTurnEnd struct {
	TurnID string
	Key    string
	Status string
	Turn   *surface.TurnMetadata
	// Reason is why a failed turn failed, such as the API refusing it.
	Reason string
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
	// between holds the compactions Claude ran while no turn was open, idle or
	// by /compact, for the turn that follows them. It is complete only when
	// betweenKnown: the tracker saw the last turn end or read from the start.
	between      []surface.TurnCompaction
	betweenKnown bool
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
	context   *int64
	// compactionsKnown holds when the meter saw everything since the last
	// turn ended, so compactions is the whole list.
	compactions      []surface.TurnCompaction
	compactionsKnown bool
	finished         bool
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
		t.meter = claudeTurnMeter{turnID: t.current, usage: map[string]surface.TurnUsage{}, compactions: t.between, compactionsKnown: t.betweenKnown}
		t.between, t.betweenKnown = nil, false
	}
	turnID := t.current
	t.observeCompaction(record)
	if turnID != "" && turnID == t.meter.turnID && !t.meter.finished {
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
		if done {
			t.between, t.betweenKnown = nil, true
		}
		return turnID, ended, false
	}
	key := stableTimelineItemID(offset, line, itemCount)
	if ending {
		at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
		t.closing = surface.ClaudeClosingTurn{TurnID: turnID, MessageID: messageID, Key: key, At: at}
	} else if done {
		if reason := claudeRecordAPIError(record); reason != "" {
			before := len(ended)
			if ended = t.finish(ended, turnID, key, "failed"); len(ended) > before {
				ended[len(ended)-1].Reason = reason
			}
		} else {
			ended = t.finish(ended, turnID, key, "")
		}
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
	t.between, t.betweenKnown = nil, true
	turn := surface.NewTurnMetadata(surface.TurnStateOfDone(status), surface.TurnMeasure{})
	if turnID == t.meter.turnID {
		turn = t.meter.metadata(status)
		t.meter.finished = true
	}
	return append(ended, claudeTurnEnd{TurnID: turnID, Key: key, Status: status, Turn: turn})
}

// observeCompaction records a compact_boundary in the open turn, or for the
// next turn when none is open. Claude writes its idle notice just after the
// boundary of an idle compaction, so the notice relabels the latest one.
func (t *claudeTurnTracker) observeCompaction(record map[string]any) {
	if str(record, "type") != "system" {
		return
	}
	open := t.current != "" && t.current == t.meter.turnID && !t.meter.finished
	compactions := &t.between
	if open {
		compactions = &t.meter.compactions
	}
	switch str(record, "subtype") {
	case "compact_boundary":
		metadata, _ := record["compactMetadata"].(map[string]any)
		compaction := surface.TurnCompaction{Trigger: surface.CompactionThreshold}
		switch str(metadata, "trigger") {
		case "manual":
			compaction.Trigger = surface.CompactionManual
		case "auto":
		default:
			compaction.Trigger = surface.CompactionUnknown
		}
		if value, ok := metadata["preTokens"]; ok {
			pre := int64Value(value)
			compaction.PreTokens = &pre
		}
		if value, ok := metadata["postTokens"]; ok {
			post := int64Value(value)
			compaction.PostTokens = &post
			if open {
				t.meter.context = &post
			}
		}
		*compactions = append(*compactions, compaction)
	case "informational":
		if n := len(*compactions); n > 0 && claudeIdleCompactionNotice(str(record, "content")) {
			(*compactions)[n-1].Trigger = surface.CompactionIdle
		}
	}
}

func claudeIdleCompactionNotice(content string) bool {
	return strings.HasPrefix(content, "Compacted while idle") || strings.HasPrefix(content, "Compacted after an hour idle")
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
	model := str(message, "model")
	if model == claudeSyntheticModel {
		return
	}
	if model != "" {
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
	context := counted.InputTokens
	m.context = &context
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
	var compactions []surface.TurnCompaction
	if m.compactionsKnown {
		compactions = append([]surface.TurnCompaction{}, m.compactions...)
	}
	return surface.NewTurnMetadata(surface.TurnStateOfDone(status), surface.TurnMeasure{Model: m.model, DurationMs: duration, Usage: usage, ContextTokens: m.context, Compactions: compactions})
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
	from, found, err := turnReplayStart(ctx, file, offset, []byte(`"user"`), claudeRecordStartsTurn)
	if err != nil || !found {
		return tracker, err
	}
	tracker.betweenKnown = from == 0
	err = scanTranscriptRecords(ctx, file, from, offset, func(record map[string]any, recordOffset int64, line []byte) {
		tracker.observe(record, recordOffset, line, len(claudeTimelineItems(record)))
	})
	return tracker, err
}

// turnReplayStart returns where to replay from to stand at offset with what
// a reader following the transcript from its start would hold: the start of
// the turn before the one in progress, which carries the compactions and the
// context size that preceded the current turn. Without an earlier turn it is
// the transcript's start when that is in reach, else the current turn's.
func turnReplayStart(ctx context.Context, file *os.File, offset int64, needle []byte, starts func(map[string]any) bool) (int64, bool, error) {
	current, found, err := transcriptRecordBefore(ctx, file, offset, needle, starts)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, offset <= turnScanLimit, nil
	}
	previous, found, err := transcriptRecordBefore(ctx, file, current, needle, starts)
	switch {
	case err != nil:
		return 0, false, err
	case found:
		return previous, true, nil
	case current <= turnScanLimit:
		return 0, true, nil
	}
	return current, true, nil
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
// items the transcript records from the requested offset to its last complete
// record, as a transcript page derives them, and where the transcript stood
// after them. A prompt recorded twice, queued and then delivered, appears
// once, at its delivery.
func (c *Claude) TranscriptItems(ctx context.Context, session *surface.Session, request surface.TranscriptItemsRequest) (*surface.TranscriptItemsResult, error) {
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
	identity := transcriptFileIdentity(info)
	if request.Identity != "" && (identity != request.Identity || request.From > info.Size()) {
		return nil, surface.ErrTranscriptChanged
	}
	end, err := completeJSONLEnd(ctx, file, info.Size())
	if err != nil {
		return nil, err
	}
	from := min(max(0, request.From), end)
	if request.MaxBytes > 0 && end-from > request.MaxBytes {
		return nil, surface.ErrTranscriptBacklog
	}
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
			items = append(items, surface.TimelineItem{ID: end.Key, Kind: "done", TurnID: end.TurnID, Status: end.Status, Text: end.Reason, Timestamp: at, Turn: end.Turn})
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
	result := &surface.TranscriptItemsResult{Items: kept, End: end, Identity: identity}
	if turns.closing.TurnID != "" {
		closing := turns.closing
		if closing.Anchor, err = completionAnchor(path, end); err != nil {
			return nil, err
		}
		result.ClaudeClosingTurn = closing
	}
	return result, nil
}
