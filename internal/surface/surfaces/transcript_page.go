package surfaces

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var timelineReadBudget int64 = 16 << 20

const timelineTextBudget = 16 << 10

const timelineItemLimit = 200

type transcriptRecord struct {
	offset int64
	line   []byte
	items  []surface.TimelineItem
}

// readTranscriptPage returns the newest page of the transcript ending at before. When limit is
// positive the page holds at most that many exchanges plus the activity recorded alongside
// them, and NextBefore always addresses the record preceding the oldest returned one.
// settled reports whether a Claude turn still closing at the end of the
// transcript has ended, given the time of its end_turn block.
func readTranscriptPage(ctx context.Context, path, source string, before int64, limit int, settled func(time.Time) bool) (*surface.SessionReadResult, error) {
	return readTranscriptWindow(ctx, path, source, 0, before, limit, settled)
}

// readTranscriptWindow reads a page that ends at before and never reaches
// below floor, a record boundary. A page that reached floor reports floor as
// its NextBefore.
func readTranscriptWindow(ctx context.Context, path, source string, floor, before int64, limit int, settled func(time.Time) bool) (*surface.SessionReadResult, error) {
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
	completeEnd, err := completeJSONLEnd(ctx, file, end)
	if err != nil {
		return nil, err
	}
	result.TranscriptOffset = completeEnd
	result.TranscriptOffsetSet = true
	result.TranscriptIdentity = transcriptFileIdentity(info)
	if floor > end {
		return nil, fmt.Errorf("activity transcript changed; refresh the session")
	}
	for window := timelineFirstWindow; ; window *= 2 {
		window = min(window, timelineReadBudget)
		page := *result
		complete, err := readTranscriptSpan(ctx, file, path, source, floor, max(floor, end-window), end, completeEnd, before, limit, settled, &page)
		if err != nil {
			return nil, err
		}
		if complete || window == timelineReadBudget || end-window <= floor {
			return &page, nil
		}
	}
}

// timelineFirstWindow is the transcript a page reads first. A page reads twice
// as much until its records stop inside what it read, up to
// timelineReadBudget, so a page costs about what it shows. Tests set it to
// the budget to read a page the way one read of the whole budget does.
var timelineFirstWindow int64 = 512 << 10
