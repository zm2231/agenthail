package codexsurface

import (
	"context"
	"os"
	"slices"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

type codexTranscript struct{}

func (codexTranscript) TurnStarts() *transcript.TurnStartSearch { return newCodexTurnStartSearch() }

func (codexTranscript) RecordItems(record map[string]any) []surface.TimelineItem {
	return codexTimelineItems(record)
}

func (codexTranscript) AttachmentReferences(record map[string]any) []transcript.AttachmentReference {
	return codexAttachmentReferences(record)
}

func (codexTranscript) TrailingAttachment(context.Context, string, map[string]any, int64, int) (transcript.AttachmentReference, bool, error) {
	return transcript.AttachmentReference{}, false, nil
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
func (codexTranscript) ReadSpan(ctx context.Context, file *os.File, span transcript.Span) (transcript.SpanItems, error) {
	s := &codexSpan{exact: span.End, turnIDs: map[int]string{}, turns: map[int]*surface.TurnMetadata{}, placed: map[int][]surface.TimelineItem{}, cutOffs: map[int][]surface.TimelineItem{}, skipResponseUsers: map[int]bool{}}
	projection := codexUserProjection{}
	currentTurnID := ""
	callRecords := map[string]int{}
	window := surface.CodexToolWindow{}
	meter, err := codexTurnMeterFrom(ctx, file, span.Start, span.Starts)
	if err != nil {
		return nil, err
	}
	lineOffset := span.Start
	for index, line := range span.Lines {
		recordOffset := lineOffset
		lineOffset += int64(len(line))
		var record map[string]any
		if transcript.DecodeCompactRecord(line, &record) != nil {
			continue
		}
		if s.exact == span.End && codexRecordStartsTurn(record) {
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
			codexForgetInput(&window, record, s.placed[index])
			s.skipResponseUsers[index] = true
		}
	}
	span.Result.CodexPendingEventUser = projection.pending
	span.Result.CodexPendingEventTurn = projection.turnID
	span.Result.CodexCurrentTurnID = currentTurnID
	span.Result.CodexToolWindow = window
	return s, nil
}

func (s *codexSpan) ExactFrom() int64 { return s.exact }

func (s *codexSpan) Items(ctx context.Context, index int, record map[string]any, offset int64, line []byte) ([]surface.TimelineItem, bool, error) {
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
	if err := transcript.DecorateTimelineAttachments(ctx, items, codexAttachmentReferences(record), offset); err != nil {
		return nil, false, err
	}
	if s.skipResponseUsers[index] {
		return nil, true, nil
	}
	for i := range items {
		items[i].ID = codexServedItemKey(record, line, offset, items[i], i)
		if items[i].Timestamp == "" {
			items[i].Timestamp = transcript.String(record, "timestamp")
		}
	}
	return items, false, nil
}

func (s *codexSpan) Reply([]transcript.Record, *surface.SessionReadResult) bool { return false }
