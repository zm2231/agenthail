package transcript

import (
	"context"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

type Format interface {
	TurnStarts() *TurnStartSearch
	RecordItems(record map[string]any) []surface.TimelineItem
	AttachmentReferences(record map[string]any) []AttachmentReference
	// trailingAttachment is the index-th attachment past the record's own timeline items.
	TrailingAttachment(ctx context.Context, path string, record map[string]any, offset int64, index int) (AttachmentReference, bool, error)
	ReadSpan(ctx context.Context, file *os.File, span Span) (SpanItems, error)
}

type Span struct {
	Path                            string
	Start, End, CompleteEnd, Before int64
	Lines                           [][]byte
	Starts                          *TurnStartSearch
	Result                          *surface.SessionReadResult
}

type SpanItems interface {
	ExactFrom() int64
	Items(ctx context.Context, index int, record map[string]any, offset int64, line []byte) (items []surface.TimelineItem, skip bool, err error)
	// reply reports whether the reply it found may continue before the span's first record.
	Reply(records []Record, result *surface.SessionReadResult) bool
}
