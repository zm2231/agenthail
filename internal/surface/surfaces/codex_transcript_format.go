package surfaces

import (
	"context"
	"os"
	"slices"

	"github.com/zm2231/agenthail/internal/surface"
)

type codexTranscript struct{}

func (codexTranscript) turnStarts() *turnStartSearch { return newCodexTurnStartSearch() }

func (codexTranscript) recordItems(record map[string]any) []surface.TimelineItem {
	return codexTimelineItems(record)
}

func (codexTranscript) attachmentReferences(record map[string]any) []attachmentReference {
	return codexAttachmentReferences(record)
}

func (codexTranscript) trailingAttachment(context.Context, string, map[string]any, int64, int) (attachmentReference, bool, error) {
	return attachmentReference{}, false, nil
}

type codexSpan struct {
	exact             int64
	turnIDs           map[int]string
	turns             map[int]*surface.TurnMetadata
	placed            map[int][]surface.TimelineItem
	cutOffs           map[int][]surface.TimelineItem
	skipResponseUsers map[int]bool
}

// Codex's turn and prompt state are known from the span's first turn start.
func (codexTranscript) readSpan(ctx context.Context, file *os.File, span transcriptSpan) (transcriptSpanItems, error) {
	s := &codexSpan{exact: span.end, turnIDs: map[int]string{}, turns: map[int]*surface.TurnMetadata{}, placed: map[int][]surface.TimelineItem{}, cutOffs: map[int][]surface.TimelineItem{}, skipResponseUsers: map[int]bool{}}
	projection := codexUserProjection{}
	currentTurnID := ""
	callRecords := map[string]int{}
	window := surface.CodexToolWindow{}
	meter, err := codexTurnMeterFrom(ctx, file, span.start, span.starts)
	if err != nil {
		return nil, err
	}
	lineOffset := span.start
	for index, line := range span.lines {
		recordOffset := lineOffset
		lineOffset += int64(len(line))
		var record map[string]any
		if decodeCompactRecord(line, &record) != nil {
			continue
		}
		if s.exact == span.end && codexRecordStartsTurn(record) {
			s.exact = recordOffset
		}
		s.cutOffs[index] = meter.cutOff(record)
		if turn := meter.observe(record); turn != nil {
			s.turns[index] = turn
		}
		if turnID := codexRecordTurnID(record); turnID != "" {
			currentTurnID = turnID
		}
		s.turnIDs[index] = currentTurnID
		s.placed[index] = codexPlaceItems(s.placed, callRecords, index, record, codexRecordItems(&window, record, currentTurnID))
		if projection.consume(record, s.placed[index], currentTurnID) {
			s.skipResponseUsers[index] = true
		}
	}
	span.result.CodexPendingEventUser = projection.pending
	span.result.CodexPendingEventTurn = projection.turnID
	span.result.CodexCurrentTurnID = currentTurnID
	span.result.CodexToolWindow = window
	return s, nil
}

func (s *codexSpan) exactFrom() int64 { return s.exact }

func (s *codexSpan) items(ctx context.Context, index int, record map[string]any, offset int64, line []byte) ([]surface.TimelineItem, bool, error) {
	items := slices.Clone(s.placed[index])
	for itemIndex := range items {
		if turnID := s.turnIDs[index]; turnID != "" {
			items[itemIndex].TurnID = turnID
		}
		if items[itemIndex].Kind == "done" {
			items[itemIndex].Turn = s.turns[index]
		}
	}
	items = append(s.cutOffs[index], items...)
	if err := decorateTimelineAttachments(ctx, items, codexAttachmentReferences(record), offset); err != nil {
		return nil, false, err
	}
	if s.skipResponseUsers[index] {
		return nil, true, nil
	}
	for i := range items {
		items[i].ID = codexServedItemKey(record, line, offset, items[i], i)
		if items[i].Timestamp == "" {
			items[i].Timestamp = str(record, "timestamp")
		}
	}
	return items, false, nil
}

func (s *codexSpan) reply([]transcriptRecord, *surface.SessionReadResult) bool { return false }
