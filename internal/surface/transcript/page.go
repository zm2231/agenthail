package transcript

import (
	"context"
	"fmt"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

var TimelineReadBudget int64 = 16 << 20

const TimelineTextBudget = 16 << 10

const TimelineItemLimit = 200

type Record struct {
	offset int64
	Line   []byte
	items  []surface.TimelineItem
}

// ReadPage returns the newest page of the transcript ending at before. When limit is
// positive the page holds at most that many exchanges plus the activity recorded alongside
// them, and NextBefore always addresses the record preceding the oldest returned one.
func ReadPage(ctx context.Context, path string, format Format, before int64, limit int) (*surface.SessionReadResult, error) {
	return ReadWindow(ctx, path, format, 0, before, limit)
}

// ReadWindow reads a page that ends at before and never reaches
// below floor, a record boundary. A page that reached floor reports floor as
// its NextBefore.
func ReadWindow(ctx context.Context, path string, format Format, floor, before int64, limit int) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Source: "local-transcript"}
	if path == "" {
		result.UnavailableReason = "Detailed activity is unavailable because this session has no local transcript."
		return result, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.UnavailableReason = "Detailed activity is unavailable because the local transcript has not been created yet."
			return result, nil
		}
		return nil, fmt.Errorf("local activity transcript is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	end := info.Size()
	if before > 0 {
		if before > end {
			return nil, fmt.Errorf("activity transcript changed; refresh the session")
		}
		end = before
	}
	completeEnd, err := CompleteJSONLEnd(ctx, file, end)
	if err != nil {
		return nil, err
	}
	result.TranscriptOffset = completeEnd
	result.TranscriptOffsetSet = true
	result.TranscriptIdentity = FileIdentity(info)
	if floor > end {
		return nil, fmt.Errorf("activity transcript changed; refresh the session")
	}
	starts := format.TurnStarts()
	for window := TimelineFirstWindow; ; window *= 2 {
		window = min(window, TimelineReadBudget)
		page := *result
		complete, err := readTranscriptSpan(ctx, file, path, format, floor, max(floor, end-window), end, completeEnd, before, limit, starts, &page)
		if err != nil {
			return nil, err
		}
		if complete || window == TimelineReadBudget || end-window <= floor {
			return &page, nil
		}
	}
}

// TimelineFirstWindow is the transcript a page reads first. A page reads twice
// as much until its records stop inside what it read, up to
// timelineReadBudget, so a page costs about what it shows. Tests set it to
// the budget to read a page the way one read of the whole budget does.
var TimelineFirstWindow int64 = 512 << 10
