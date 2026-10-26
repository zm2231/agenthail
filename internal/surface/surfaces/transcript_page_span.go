package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

// readTranscriptSpan fills result with the page that ends at end from the
// records after start. It reports whether a larger span would give the same
// page: the page stopped at a record its own limits chose, after the first
// record whose state the span knows exactly.
func readTranscriptSpan(ctx context.Context, file *os.File, path, source string, floor, start, end, completeEnd, before int64, limit int, settled func(time.Time) bool, result *surface.SessionReadResult) (bool, error) {
	var err error
	windowStart := start
	data := make([]byte, end-start)
	if _, err = file.ReadAt(data, start); err != nil && err != io.EOF {
		return false, err
	}
	if start > floor {
		if first := bytes.IndexByte(data, '\n'); first >= 0 {
			start += int64(first + 1)
			data = data[first+1:]
		} else {
			result.Truncated = true
			result.NextBefore = start
			return false, nil
		}
	}
	// Incomplete appends are retried by the next refresh, never rendered as records.
	if last := bytes.LastIndexByte(data, '\n'); last >= 0 {
		data = data[:last+1]
	} else {
		data = nil
	}
	lines := bytes.SplitAfter(data, []byte{'\n'})
	projection := codexUserProjection{}
	skipResponseUsers := map[int]bool{}
	recordTurnIDs := map[int]string{}
	currentTurnID := ""
	codexTurns := map[int]*surface.TurnMetadata{}
	codexItems := map[int][]surface.TimelineItem{}
	window := surface.CodexToolWindow{}
	// Claude's turn state is rebuilt at start; Codex's turn and prompt state
	// are known from the span's first turn start.
	exactFrom := start
	if source == "codex" {
		exactFrom = end
		meter, err := codexTurnMeterAt(ctx, file, start)
		if err != nil {
			return false, err
		}
		lineOffset := start
		for index, line := range lines {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				continue
			}
			if exactFrom == end && codexRecordStartsTurn(record) {
				exactFrom = recordOffset
			}
			if turn := meter.observe(record); turn != nil {
				codexTurns[index] = turn
			}
			if turnID := codexRecordTurnID(record); turnID != "" {
				currentTurnID = turnID
			}
			recordTurnIDs[index] = currentTurnID
			codexItems[index] = codexRecordItems(&window, record, currentTurnID)
			if projection.consume(record, codexItems[index], currentTurnID) {
				skipResponseUsers[index] = true
			}
		}
	}
	result.CodexPendingEventUser = projection.pending
	result.CodexPendingEventTurn = projection.turnID
	result.CodexCurrentTurnID = currentTurnID
	result.CodexToolWindow = window
	var claudeEnds map[int][]claudeTurnEnd
	var claudeInterrupts map[int]bool
	if source == "claude" {
		turns, err := claudeTurnStateAt(ctx, file, start)
		if err != nil {
			return false, err
		}
		claudeEnds = map[int][]claudeTurnEnd{}
		claudeInterrupts = map[int]bool{}
		lineOffset, last := start, -1
		for index, line := range lines {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				continue
			}
			turnID, ended, interrupted := turns.observe(record, recordOffset, line, len(claudeTimelineItems(record)))
			recordTurnIDs[index] = turnID
			claudeEnds[index] = ended
			claudeInterrupts[index] = interrupted
			last = index
		}
		if before <= 0 && last >= 0 {
			claudeEnds[last] = append(claudeEnds[last], turns.settle(settled)...)
		}
		if before <= 0 && turns.closing.TurnID != "" {
			// A stream resuming at the page's end finishes the turn the page saw closing.
			closing := turns.closing
			if closing.Anchor, err = completionAnchor(path, completeEnd); err != nil {
				return false, err
			}
			result.ClaudeClosingTurn = closing
		}
	}
	offset := start + int64(len(data))
	budget := 512 << 10
	var groups [][]surface.TimelineItem
	var groupOffsets []int64
	var records []transcriptRecord
	// truncatedAt holds the records the page shortened or dropped; only those
	// on the page mark it truncated.
	var truncatedAt []int64
	stopped := false
	for index := len(lines) - 1; index >= 0; index-- {
		line := lines[index]
		offset -= int64(len(line))
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			truncatedAt = append(truncatedAt, offset)
			continue
		}
		var items []surface.TimelineItem
		if source == "claude" {
			items, err = claudeRecordTimelineItems(ctx, record, offset, line, recordTurnIDs[index], claudeEnds[index], claudeInterrupts[index])
			if err != nil {
				return false, err
			}
		} else {
			items = slices.Clone(codexItems[index])
			for itemIndex := range items {
				if turnID := recordTurnIDs[index]; turnID != "" {
					items[itemIndex].TurnID = turnID
				}
				if items[itemIndex].Kind == "done" {
					items[itemIndex].Turn = codexTurns[index]
				}
			}
			if err := decorateTimelineAttachments(ctx, items, record, source, offset); err != nil {
				return false, err
			}
			if skipResponseUsers[index] {
				continue
			}
			for i := range items {
				items[i].ID = codexServedItemKey(record, line, offset, items[i], i)
				items[i].Timestamp = str(record, "timestamp")
			}
		}
		if len(items) == 0 {
			// Records without items still close a Claude turn whose last block was written.
			records = append(records, transcriptRecord{offset: offset, line: line})
			continue
		}
		full := slices.Clone(items)
		for i := range items {
			item := &items[i]
			if len(item.Text) > timelineTextBudget {
				cut := timelineTextBudget
				for cut > 0 && !utf8.RuneStart(item.Text[cut]) {
					cut--
				}
				item.Text = item.Text[:cut]
				item.Truncated = true
				item.TruncationReason = "timeline text limit"
			}
		}
		encoded, _ := json.Marshal(items)
		count := 0
		for _, group := range groups {
			count += len(group)
		}
		if len(groups) > 0 && (len(encoded) > budget || count+len(items) > timelineItemLimit) {
			result.NextBefore = offset + int64(len(line))
			stopped = true
			break
		}
		records = append(records, transcriptRecord{offset: offset, line: line, items: full})
		// A single oversized record must not defeat the response budget.
		for len(encoded) > budget && len(items) > 1 {
			items = items[1:]
			truncatedAt = append(truncatedAt, offset)
			encoded, _ = json.Marshal(items)
		}
		if len(encoded) > budget {
			truncatedAt = append(truncatedAt, offset)
			continue
		}
		for _, item := range items {
			if item.Truncated {
				truncatedAt = append(truncatedAt, offset)
			}
		}
		groups = append(groups, items)
		groupOffsets = append(groupOffsets, offset)
		budget -= len(encoded)
	}
	if result.NextBefore == 0 && start > 0 {
		if start == end {
			result.Truncated = true
			result.NextBefore = windowStart
		} else {
			result.NextBefore = start
		}
	}
	slices.Reverse(records)
	var items []surface.TimelineItem
	var itemOffsets []int64
	seen := map[string]bool{}
	for i := len(records) - 1; i >= 0; i-- {
		for j := len(records[i].items) - 1; j >= 0; j-- {
			if item := records[i].items[j]; !seen[item.ID] {
				seen[item.ID] = true
				items = append(items, item)
				itemOffsets = append(itemOffsets, records[i].offset)
			}
		}
	}
	slices.Reverse(items)
	slices.Reverse(itemOffsets)
	result.Exchanges = surface.ExchangesFromItems(items, "")
	var exchangeOffsets []int64
	for _, start := range surface.ExchangeStarts(items) {
		exchangeOffsets = append(exchangeOffsets, itemOffsets[start])
	}
	// Records that reached the span's start may continue before it: a reply
	// from their first turn may be that turn's tail, and no reply may only
	// mean it is further back.
	replyKnown := true
	if source == "claude" {
		var from int
		result.Reply, from = claudeTranscriptReply(records, result.Source, settled)
		replyKnown = stopped || from > 0 || start <= floor
	}
	pageStart := int64(-1)
	if limit > 0 && len(result.Exchanges) > limit {
		result.Exchanges = result.Exchanges[len(result.Exchanges)-limit:]
		pageStart = exchangeOffsets[len(exchangeOffsets)-limit]
		result.NextBefore = pageStart
	}
	// An item recorded twice, as a queued and a delivered prompt, keeps its
	// newest copy, as the journal does.
	seen = map[string]bool{}
	for i := 0; i < len(groups); i++ {
		if groupOffsets[i] < pageStart {
			continue
		}
		for j := len(groups[i]) - 1; j >= 0; j-- {
			if item := groups[i][j]; !seen[item.ID] {
				seen[item.ID] = true
				result.Items = append(result.Items, item)
			}
		}
	}
	slices.Reverse(result.Items)
	for _, at := range truncatedAt {
		if at >= pageStart {
			result.Truncated = true
		}
	}
	used := result.NextBefore
	if !stopped {
		used = pageStart
	}
	return replyKnown && used >= 0 && used >= exactFrom && (stopped || pageStart >= 0), nil
}
