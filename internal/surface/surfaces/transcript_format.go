package surfaces

import (
	"context"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

type transcriptFormat interface {
	turnStarts() *turnStartSearch
	recordItems(record map[string]any) []surface.TimelineItem
	attachmentReferences(record map[string]any) []attachmentReference
	// trailingAttachment is the index-th attachment past the record's own timeline items.
	trailingAttachment(ctx context.Context, path string, record map[string]any, offset int64, index int) (attachmentReference, bool, error)
	readSpan(ctx context.Context, file *os.File, span transcriptSpan) (transcriptSpanItems, error)
}

type transcriptSpan struct {
	path                            string
	start, end, completeEnd, before int64
	lines                           [][]byte
	starts                          *turnStartSearch
	result                          *surface.SessionReadResult
}

type transcriptSpanItems interface {
	exactFrom() int64
	items(ctx context.Context, index int, record map[string]any, offset int64, line []byte) (items []surface.TimelineItem, skip bool, err error)
	// reply reports whether the reply it found may continue before the span's first record.
	reply(records []transcriptRecord, result *surface.SessionReadResult) bool
}
