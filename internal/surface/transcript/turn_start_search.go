package transcript

import (
	"context"
	"os"
	"slices"
)

// TurnStartSearch finds the last record before an offset that match accepts,
// within turnScanLimit bytes. It keeps what it scanned, so asking again at
// an earlier offset, as a page widening its window does, reads only the
// bytes below what it already read.
type TurnStartSearch struct {
	candidate func([]byte) bool
	decode    func([]byte, *map[string]any) error
	match     func(map[string]any) bool
	scanned   bool
	floor     int64
	lo        int64
	hi        int64
	found     []int64
}

func NewTurnStartSearch(candidate func([]byte) bool, decode func([]byte, *map[string]any) error, match func(map[string]any) bool) *TurnStartSearch {
	return &TurnStartSearch{candidate: candidate, decode: decode, match: match}
}

func (s *TurnStartSearch) before(ctx context.Context, file *os.File, offset int64) (int64, bool, error) {
	floor := max(0, offset-TurnScanLimit)
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
		if s.decode(line, &record) == nil && s.match(record) {
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
