package surfaces

import (
	"context"
	"os"
	"slices"
)

// turnStartSearch finds the last record before an offset that match accepts,
// within turnScanLimit bytes. It keeps what it scanned, so asking again at
// an earlier offset, as a page widening its window does, reads only the
// bytes below what it already read.
type turnStartSearch struct {
	candidate func([]byte) bool
	match     func(map[string]any) bool
	scanned   bool
	floor     int64
	lo        int64
	hi        int64
	found     []int64
}

func newTurnStartSearch(candidate func([]byte) bool, match func(map[string]any) bool) *turnStartSearch {
	return &turnStartSearch{candidate: candidate, match: match}
}

func (s *turnStartSearch) before(ctx context.Context, file *os.File, offset int64) (int64, bool, error) {
	floor := max(0, offset-turnScanLimit)
	if !s.scanned || offset > s.hi || offset < s.lo {
		s.scanned, s.floor, s.lo, s.hi, s.found = true, offset, offset, offset, nil
	}
	for index := len(s.found) - 1; index >= 0; index-- {
		if s.found[index] < offset {
			return s.found[index], s.found[index] >= floor, nil
		}
	}
	if s.floor <= floor {
		return 0, false, nil
	}
	at, hit := int64(0), false
	err := reverseTranscriptLines(ctx, file, floor, s.lo, func(start int64, line []byte) bool {
		s.lo = start
		if !s.candidate(line) {
			return true
		}
		var record map[string]any
		if decodeShrunkRecord(line, &record) == nil && s.match(record) {
			at, hit = start, true
			return false
		}
		return true
	})
	if err != nil {
		s.scanned = false
		return 0, false, err
	}
	if !hit {
		s.floor = floor
		return 0, false, nil
	}
	s.floor = at
	s.found = slices.Insert(s.found, 0, at)
	return at, true, nil
}
