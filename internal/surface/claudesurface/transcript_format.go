package claudesurface

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

// claudeTranscript reads a Claude transcript; settled reports whether a turn
// still closing at the end of the transcript has ended, given the time of its
// end_turn block.
type claudeTranscript struct {
	settled func(time.Time) bool
}

func (claudeTranscript) TurnStarts() *transcript.TurnStartSearch { return newClaudeTurnStartSearch() }

func (claudeTranscript) RecordItems(record map[string]any) []surface.TimelineItem {
	return claudeTimelineItems(record)
}

func (claudeTranscript) AttachmentReferences(record map[string]any) []transcript.AttachmentReference {
	return claudeAttachmentReferences(record)
}

func (claudeTranscript) TrailingAttachment(ctx context.Context, path string, record map[string]any, offset int64, index int) (transcript.AttachmentReference, bool, error) {
	_, files, err := verifiedSentFiles(ctx, newClaudeToolCalls(path), record, offset)
	if err != nil {
		return transcript.AttachmentReference{}, false, err
	}
	if index < len(files) {
		return sentFileReference(files[index]), true, nil
	}
	return transcript.AttachmentReference{}, false, nil
}

type claudeSpan struct {
	start      int64
	settled    func(time.Time) bool
	calls      *claudeToolCalls
	turnIDs    map[int]string
	ends       map[int][]claudeTurnEnd
	interrupts map[int]bool
}

// Claude's turn state is rebuilt at the span's start.
func (t claudeTranscript) ReadSpan(ctx context.Context, file *os.File, span transcript.Span) (transcript.SpanItems, error) {
	s := &claudeSpan{start: span.Start, settled: t.settled, calls: newClaudeToolCalls(span.Path), turnIDs: map[int]string{}, ends: map[int][]claudeTurnEnd{}, interrupts: map[int]bool{}}
	turns, err := claudeTurnStateFrom(ctx, file, span.Start, span.Starts)
	if err != nil {
		return nil, err
	}
	lineOffset, last := span.Start, -1
	for index, line := range span.Lines {
		recordOffset := lineOffset
		lineOffset += int64(len(line))
		var record map[string]any
		if transcript.DecodeCompactRecord(line, &record) != nil {
			continue
		}
		s.calls.observe(record, recordOffset)
		turnID, ended, interrupted := turns.observe(record, recordOffset, line, func() int { return len(claudeTimelineItems(record)) })
		s.turnIDs[index] = turnID
		s.ends[index] = ended
		s.interrupts[index] = interrupted
		last = index
	}
	if span.Before <= 0 && last >= 0 {
		s.ends[last] = append(s.ends[last], turns.settle(t.settled)...)
	}
	if span.Before <= 0 && turns.closing.TurnID != "" {
		// A stream resuming at the page's end finishes the turn the page saw closing.
		closing := turns.closing
		if closing.Anchor, err = completionAnchor(span.Path, span.CompleteEnd); err != nil {
			return nil, err
		}
		span.Result.ClaudeClosingTurn = closing
	}
	return s, nil
}

func (s *claudeSpan) ExactFrom() int64 { return s.start }

func (s *claudeSpan) Items(ctx context.Context, index int, record map[string]any, offset int64, line []byte) ([]surface.TimelineItem, bool, error) {
	items, err := claudeRecordTimelineItems(ctx, s.calls, record, offset, line, s.turnIDs[index], s.ends[index], s.interrupts[index])
	return items, false, err
}

func (s *claudeSpan) Reply(records []transcript.Record, result *surface.SessionReadResult) bool {
	var from int
	result.Reply, from = claudeTranscriptReply(records, result.Source, s.settled)
	return from == 0
}
