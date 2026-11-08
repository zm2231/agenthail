package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
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
	// A /compact over Remote Control comes before its boundary; one the SDK
	// runs comes after it, sharing its promptId with the boundary's summary.
	compactRequest string
	summaryPrompt  string
	interleaved    bool
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
	// answered holds once Claude replied in the turn, and endBlock once a
	// reply ended it; a turn answered without an end block is still open.
	answered bool
	endBlock bool
	// seenBefore holds, on a tracker resumed past a checkpoint region's
	// verified offset, the messages that region had counted.
	lastUsage   string
	interleaved bool
	seenBefore  map[string]bool
}

func newClaudeTurnTracker(current string, closing surface.ClaudeClosingTurn) *claudeTurnTracker {
	return &claudeTurnTracker{current: current, closing: closing, terminal: map[string]bool{}}
}

// observe returns the record's turn, the turns that ended at it, and whether
// it interrupted its turn. A done key names the record that ended the turn
// and the item index after its items, as the live stream has always keyed it.
func (t *claudeTurnTracker) observe(record map[string]any, offset int64, line []byte, itemCount func() int) (string, []claudeTurnEnd, bool) {
	var ended []claudeTurnEnd
	messageID := strNested(record, "message", "id")
	if t.closing.TurnID != "" && !claudeRecordContinuesMessage(str(record, "type"), messageID, t.closing.MessageID) {
		if !claudeRecordIsInterrupt(record) {
			ended = t.finish(ended, t.closing.TurnID, t.closing.Key, "")
		}
		t.closing = surface.ClaudeClosingTurn{}
	}
	if claudeRecordStartsTurn(record) && !(claudeRecordIsStopHookFeedback(record) && t.answeredWithoutEnd(t.current)) {
		if t.answeredWithoutEnd(t.current) {
			ended = t.finish(ended, t.current, stableTimelineItemID(offset, line, itemCount()), "")
		}
		t.current = str(record, "uuid")
		t.interleaved = t.sawInterleaving()
		t.meter = claudeTurnMeter{turnID: t.current, usage: map[string]surface.TurnUsage{}, compactions: t.between, compactionsKnown: t.betweenKnown}
		t.between, t.betweenKnown = nil, false
	}
	turnID := t.current
	t.observeCompaction(record)
	if turnID != "" && turnID == t.meter.turnID && !t.meter.finished {
		t.meter.observe(record)
	}
	if t.answeredWithoutEnd(turnID) && claudeRecordStopsAfterHooks(record) {
		return turnID, t.finish(ended, turnID, stableTimelineItemID(offset, line, itemCount()), ""), false
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
	if !ending && !done {
		return turnID, ended, false
	}
	key := stableTimelineItemID(offset, line, itemCount())
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

// answeredWithoutEnd reports a turn Claude replied in whose last reply was
// empty, so no end_turn block or turn_duration will end it.
func (t *claudeTurnTracker) answeredWithoutEnd(turnID string) bool {
	return turnID != "" && turnID == t.meter.turnID && !t.meter.finished && t.meter.answered && !t.meter.endBlock
}

func (t *claudeTurnTracker) finish(ended []claudeTurnEnd, turnID, key, status string) []claudeTurnEnd {
	if t.terminal[turnID] || (turnID == t.meter.turnID && t.meter.finished) {
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
	open := t.current != "" && t.current == t.meter.turnID && !t.meter.finished
	compactions := &t.between
	if open {
		compactions = &t.meter.compactions
	}
	if str(record, "type") == "user" {
		t.observeCompactRequest(record, *compactions)
		return
	}
	if str(record, "type") != "system" {
		return
	}
	switch str(record, "subtype") {
	case "compact_boundary":
		metadata, _ := record["compactMetadata"].(map[string]any)
		compaction := surface.TurnCompaction{Trigger: surface.CompactionThreshold, RequestID: t.compactRequest}
		t.compactRequest, t.summaryPrompt = "", ""
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

func (t *claudeTurnTracker) observeCompactRequest(record map[string]any, compactions []surface.TurnCompaction) {
	prompt := str(record, "promptId")
	if summary, _ := record["isCompactSummary"].(bool); summary {
		t.summaryPrompt = prompt
		return
	}
	message, _ := record["message"].(map[string]any)
	if !isClaudeCompactCommand(transcriptText(message["content"])) {
		return
	}
	if n := len(compactions); n > 0 && compactions[n-1].RequestID == "" && prompt != "" && prompt == t.summaryPrompt {
		compactions[n-1].RequestID = str(record, "uuid")
		t.summaryPrompt = ""
		return
	}
	t.compactRequest = str(record, "uuid")
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
	apiError, _ := record["isApiErrorMessage"].(bool)
	m.answered = true
	m.endBlock = m.endBlock || apiError || str(message, "stop_reason") == "end_turn"
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
	if id != m.lastUsage {
		if _, again := m.usage[id]; again || m.seenBefore[id] {
			m.interleaved = true
		}
	}
	m.lastUsage = id
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
	return claudeTurnStateFrom(ctx, file, offset, newClaudeTurnStartSearch())
}

func newClaudeTurnStartSearch() *turnStartSearch {
	return newTurnStartSearch(claudeLineMayStartTurn, claudeSearchStartsTurn)
}

func claudeSearchStartsTurn(record map[string]any) bool {
	return claudeRecordStartsTurn(record) && !claudeRecordIsStopHookFeedback(record)
}

func claudeTurnStateFrom(ctx context.Context, file *os.File, offset int64, starts *turnStartSearch) (*claudeTurnTracker, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	identity := transcriptFileIdentity(info)
	if identity != "" {
		if tracker, ok, err := claudeReplayCheckpoints.resume(ctx, file, identity, info.Size(), offset); err != nil || ok {
			return tracker, err
		}
	}
	tracker := newClaudeTurnTracker("", surface.ClaudeClosingTurn{})
	from, found, err := turnReplayStart(ctx, file, offset, starts)
	if err != nil || !found {
		return tracker, err
	}
	tracker.betweenKnown = from == 0
	recorder := newReplayRecorder()
	err = scanReplayLines(ctx, file, from, offset, func(line []byte, recordOffset int64) bool {
		record, exact := decodeReplayRecord(line)
		if record == nil {
			return true
		}
		recorder.visit(tracker, record, line, recordOffset)
		tracker.observe(record, recordOffset, line, replayItemCount(record, exact, line))
		return true
	})
	if err == nil {
		claudeReplayCheckpoints.record(file, identity, offset, tracker, recorder)
	}
	return tracker, err
}

func replayItemCount(record map[string]any, exact bool, line []byte) func() int {
	return func() int {
		if exact {
			return len(claudeTimelineItems(record))
		}
		var full map[string]any
		if decodeShrunkRecord(line, &full) != nil {
			return 0
		}
		return len(claudeTimelineItems(full))
	}
}

const replayStringBytes = 256

// decodeReplayRecord cuts long strings only from records whose text the
// replay never reads; a user message with text and an API error are decoded
// as every other reader decodes them, so commands, interrupts and stop-hook
// feedback are recognised from their whole text. exact reports which.
func decodeReplayRecord(line []byte) (record map[string]any, exact bool) {
	cut, shortened := cutJSONStrings(line, replayStringBytes)
	if shortened && json.Unmarshal(cut, &record) == nil && !claudeReplayReadsText(record) {
		return record, false
	}
	record = nil
	if decodeShrunkRecord(line, &record) != nil {
		return nil, false
	}
	return record, true
}

func claudeReplayReadsText(record map[string]any) bool {
	switch str(record, "type") {
	case "user":
		message, _ := record["message"].(map[string]any)
		switch content := message["content"].(type) {
		case string:
			return true
		case []any:
			for _, raw := range content {
				if block, _ := raw.(map[string]any); str(block, "type") == "text" {
					return true
				}
			}
		}
		return false
	case "assistant":
		apiError, _ := record["isApiErrorMessage"].(bool)
		return apiError
	}
	return false
}

// scanReplayLines passes visit the reader's own buffer, valid only until visit
// returns; visit returns false to stop.
func scanReplayLines(ctx context.Context, file *os.File, from, to int64, visit func(line []byte, offset int64) bool) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, from, to-from), 1<<20)
	offset := from
	var long []byte
	pending := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long, pending = append(long, chunk...), true
			continue
		}
		line := chunk
		if pending {
			long = append(long, chunk...)
			line, pending = long, false
		}
		if len(line) > 0 && line[len(line)-1] == '\n' && !visit(line, offset) {
			return nil
		}
		offset += int64(len(line))
		long = long[:0]
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// turnReplayStart returns where to replay from to stand at offset with what
// a reader following the transcript from its start would hold: the start of
// the turn before the one in progress, which carries the compactions and the
// context size that preceded the current turn. Without an earlier turn it is
// the transcript's start when that is in reach, else the current turn's.
func turnReplayStart(ctx context.Context, file *os.File, offset int64, starts *turnStartSearch) (int64, bool, error) {
	current, found, err := starts.before(ctx, file, offset)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, offset <= turnScanLimit, nil
	}
	previous, found, err := starts.before(ctx, file, current)
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

func transcriptRecordBefore(ctx context.Context, file *os.File, offset int64, candidate func([]byte) bool, match func(map[string]any) bool) (int64, bool, error) {
	return newTurnStartSearch(candidate, match).before(ctx, file, offset)
}

func lineContains(needle []byte) func([]byte) bool {
	return func(line []byte) bool { return bytes.Contains(line, needle) }
}

func scanTranscriptRecords(ctx context.Context, file *os.File, from, to int64, compact bool, visit func(record map[string]any, offset int64, line []byte)) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, from, to-from), 1<<20)
	offset := from
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			decoded := line
			if compact {
				decoded = shrinkTranscriptLine(line)
			}
			var record map[string]any
			if json.Unmarshal(decoded, &record) == nil {
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
	if request.Identity != "" && errors.Is(err, fs.ErrNotExist) {
		return nil, surface.ErrTranscriptChanged
	}
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
	err = scanTranscriptRecords(ctx, file, from, end, false, func(record map[string]any, offset int64, line []byte) {
		if itemErr != nil {
			return
		}
		turnID, ended, interrupted := turns.observe(record, offset, line, func() int { return len(claudeTimelineItems(record)) })
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
