package surfaces

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// claudeTranscript reads a Claude transcript; settled reports whether a turn
// still closing at the end of the transcript has ended, given the time of its
// end_turn block.
type claudeTranscript struct {
	settled func(time.Time) bool
}

func (claudeTranscript) turnStarts() *turnStartSearch { return newClaudeTurnStartSearch() }

func (claudeTranscript) recordItems(record map[string]any) []surface.TimelineItem {
	return claudeTimelineItems(record)
}

func (claudeTranscript) attachmentReferences(record map[string]any) []attachmentReference {
	return claudeAttachmentReferences(record)
}

func (claudeTranscript) trailingAttachment(ctx context.Context, path string, record map[string]any, offset int64, index int) (attachmentReference, bool, error) {
	_, files, err := verifiedSentFiles(ctx, newClaudeToolCalls(path), record, offset)
	if err != nil {
		return attachmentReference{}, false, err
	}
	if index < len(files) {
		return sentFileReference(files[index]), true, nil
	}
	return attachmentReference{}, false, nil
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
func (t claudeTranscript) readSpan(ctx context.Context, file *os.File, span transcriptSpan) (transcriptSpanItems, error) {
	s := &claudeSpan{start: span.start, settled: t.settled, calls: newClaudeToolCalls(span.path), turnIDs: map[int]string{}, ends: map[int][]claudeTurnEnd{}, interrupts: map[int]bool{}}
	turns, err := claudeTurnStateFrom(ctx, file, span.start, span.starts)
	if err != nil {
		return nil, err
	}
	lineOffset, last := span.start, -1
	for index, line := range span.lines {
		recordOffset := lineOffset
		lineOffset += int64(len(line))
		var record map[string]any
		if decodeCompactRecord(line, &record) != nil {
			continue
		}
		s.calls.observe(record, recordOffset)
		turnID, ended, interrupted := turns.observe(record, recordOffset, line, func() int { return len(claudeTimelineItems(record)) })
		s.turnIDs[index] = turnID
		s.ends[index] = ended
		s.interrupts[index] = interrupted
		last = index
	}
	if span.before <= 0 && last >= 0 {
		s.ends[last] = append(s.ends[last], turns.settle(t.settled)...)
	}
	if span.before <= 0 && turns.closing.TurnID != "" {
		// A stream resuming at the page's end finishes the turn the page saw closing.
		closing := turns.closing
		if closing.Anchor, err = completionAnchor(span.path, span.completeEnd); err != nil {
			return nil, err
		}
		span.result.ClaudeClosingTurn = closing
	}
	return s, nil
}

func (s *claudeSpan) exactFrom() int64 { return s.start }

func (s *claudeSpan) items(ctx context.Context, index int, record map[string]any, offset int64, line []byte) ([]surface.TimelineItem, bool, error) {
	items, err := claudeRecordTimelineItems(ctx, s.calls, record, offset, line, s.turnIDs[index], s.ends[index], s.interrupts[index])
	return items, false, err
}

func (s *claudeSpan) reply(records []transcriptRecord, result *surface.SessionReadResult) bool {
	var from int
	result.Reply, from = claudeTranscriptReply(records, result.Source, s.settled)
	return from == 0
}
