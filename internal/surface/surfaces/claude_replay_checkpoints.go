package surfaces

import (
	"bytes"
	"cmp"
	"context"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

// replayCheckpointSpacing is how many bytes of replay lie between saved
// trackers; tests lower it.
var replayCheckpointSpacing int64 = 4 << 20

const (
	replayRegionLimit       = 8
	replayRegionCheckpoints = 16
	replayPrintBytes        = 256
)

// replayReportedLimit bounds the transcripts remembered as reported; once
// that many are, the set starts over and a transcript may be reported again.
const replayReportedLimit = 1024

// A replayRegion holds trackers saved while replaying one turn. Any offset
// in that turn has the same replay start, so a replay there resumes from the
// nearest saved tracker. Checkpoints fold counted messages into one total, so
// seen keeps the ids meterTurn had counted by verified: a replay past
// verified checks returning ids against it.
type replayRegion struct {
	identity    string
	limit       int64
	start       int64
	startPrint  []byte
	checkpoints []replayCheckpoint
	verified    int64
	meterTurn   string
	seen        map[string]bool
	used        uint64
}

// A replayCheckpoint is the tracker before the record at offset, whose first
// bytes are print.
type replayCheckpoint struct {
	offset  int64
	print   []byte
	tracker *claudeTurnTracker
}

type replayCheckpointCache struct {
	mu       sync.Mutex
	regions  []*replayRegion
	clock    uint64
	reported map[string]bool
}

var claudeReplayCheckpoints = &replayCheckpointCache{reported: map[string]bool{}}

type replayResumePoint struct {
	start      int64
	startPrint []byte
	at         replayCheckpoint
	last       int64
	verified   int64
	meterTurn  string
	seen       map[string]bool
}

func (c *replayCheckpointCache) lookup(identity string, offset int64) (replayResumePoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var best *replayRegion
	index := -1
	for _, region := range c.regions {
		if region.identity != identity || region.limit != turnScanLimit || region.start < max(0, offset-turnScanLimit) {
			continue
		}
		at, found := slices.BinarySearchFunc(region.checkpoints, offset, func(checkpoint replayCheckpoint, offset int64) int {
			return cmp.Compare(checkpoint.offset, offset)
		})
		if !found {
			at--
		}
		if at >= 0 && (best == nil || region.checkpoints[at].offset > best.checkpoints[index].offset) {
			best, index = region, at
		}
	}
	if best == nil {
		return replayResumePoint{}, false
	}
	c.clock++
	best.used = c.clock
	at := best.checkpoints[index]
	at.tracker = at.tracker.checkpoint()
	return replayResumePoint{start: best.start, startPrint: best.startPrint, at: at, last: best.checkpoints[len(best.checkpoints)-1].offset, verified: best.verified, meterTurn: best.meterTurn, seen: best.seen}, true
}

// resume returns false when no checkpoint applies: none lies in reach before
// offset, a turn starts after it, the file is shorter than the region was
// verified to or no longer holds the records it was taken at, or a message's
// usage returns after another's. Like the other transcript cursors, it takes
// a file of the same identity that still reaches its offsets as the same
// file grown by appends.
func (c *replayCheckpointCache) resume(ctx context.Context, file *os.File, identity string, size, offset int64) (*claudeTurnTracker, bool, error) {
	point, ok := c.lookup(identity, offset)
	if !ok {
		return nil, false, nil
	}
	if size < point.verified || !replayPrintMatches(file, point.start, point.startPrint) || !replayPrintMatches(file, point.at.offset, point.at.print) {
		c.drop(identity, point.start)
		return nil, false, nil
	}
	tracker := point.at.tracker
	recorder := &replayRecorder{start: point.start, startPrint: point.startPrint, last: point.last}
	usable, crossed := true, false
	err := scanReplayLines(ctx, file, point.at.offset, offset, func(line []byte, recordOffset int64) bool {
		record, exact := decodeReplayRecord(line)
		if record == nil {
			return true
		}
		if recorder.visit(tracker, record, line, recordOffset) {
			usable = false
			return false
		}
		if !crossed && recordOffset >= point.verified {
			crossed = true
			if tracker.meter.turnID == point.meterTurn {
				tracker.meter.seenBefore = point.seen
			}
		}
		tracker.observe(record, recordOffset, line, replayItemCount(record, exact, line))
		usable = !tracker.sawInterleaving()
		return usable
	})
	if err != nil || !usable {
		return nil, false, err
	}
	c.extend(identity, point.start, offset, recorder.checkpoints, tracker)
	return tracker, true, nil
}

func (c *replayCheckpointCache) record(file *os.File, identity string, offset int64, tracker *claudeTurnTracker, recorder *replayRecorder) {
	if identity == "" || recorder.start < 0 || recorder.start < max(0, offset-turnScanLimit) {
		return
	}
	if tracker.sawInterleaving() {
		c.drop(identity, recorder.start)
		c.report(identity, file.Name(), recorder.start)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clock++
	for _, region := range c.regions {
		if region.identity == identity && region.limit == turnScanLimit && region.start == recorder.start && bytes.Equal(region.startPrint, recorder.startPrint) {
			region.checkpoints = mergeReplayCheckpoints(region.checkpoints, recorder.checkpoints)
			if offset > region.verified {
				region.verified, region.meterTurn, region.seen = offset, tracker.meter.turnID, tracker.meter.usageIDs(nil)
			}
			region.used = c.clock
			return
		}
	}
	c.regions = slices.DeleteFunc(c.regions, func(region *replayRegion) bool {
		return region.identity == identity && region.start == recorder.start
	})
	if len(recorder.checkpoints) == 0 {
		return
	}
	c.regions = append(c.regions, &replayRegion{
		identity:    identity,
		limit:       turnScanLimit,
		start:       recorder.start,
		startPrint:  recorder.startPrint,
		checkpoints: mergeReplayCheckpoints(nil, recorder.checkpoints),
		verified:    offset,
		meterTurn:   tracker.meter.turnID,
		seen:        tracker.meter.usageIDs(nil),
		used:        c.clock,
	})
	if len(c.regions) > replayRegionLimit {
		oldest := 0
		for index, region := range c.regions {
			if region.used < c.regions[oldest].used {
				oldest = index
			}
		}
		c.regions = slices.Delete(c.regions, oldest, oldest+1)
	}
}

func (c *replayCheckpointCache) extend(identity string, start, offset int64, checkpoints []replayCheckpoint, tracker *claudeTurnTracker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, region := range c.regions {
		if region.identity != identity || region.start != start || region.limit != turnScanLimit {
			continue
		}
		region.checkpoints = mergeReplayCheckpoints(region.checkpoints, checkpoints)
		if offset > region.verified {
			var before map[string]bool
			if tracker.meter.turnID == region.meterTurn {
				before = region.seen
			}
			region.verified, region.meterTurn, region.seen = offset, tracker.meter.turnID, tracker.meter.usageIDs(before)
		}
		return
	}
}

func (c *replayCheckpointCache) drop(identity string, start int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.regions = slices.DeleteFunc(c.regions, func(region *replayRegion) bool {
		return region.identity == identity && region.start == start
	})
}

func (c *replayCheckpointCache) report(identity, path string, start int64) {
	c.mu.Lock()
	reported := c.reported[identity]
	if !reported && len(c.reported) >= replayReportedLimit {
		clear(c.reported)
	}
	c.reported[identity] = true
	c.mu.Unlock()
	if !reported {
		log.Printf("transcript %s: the turn at offset %d interleaves message usage; its pages replay it in full", filepath.Base(path), start)
	}
}

func mergeReplayCheckpoints(have, add []replayCheckpoint) []replayCheckpoint {
	merged := slices.Clone(have)
	for _, checkpoint := range add {
		at, found := slices.BinarySearchFunc(merged, checkpoint.offset, func(have replayCheckpoint, offset int64) int {
			return cmp.Compare(have.offset, offset)
		})
		if !found {
			merged = slices.Insert(merged, at, checkpoint)
		}
	}
	if len(merged) > replayRegionCheckpoints {
		merged = merged[len(merged)-replayRegionCheckpoints:]
	}
	return merged
}

func replayPrintMatches(file *os.File, offset int64, print []byte) bool {
	read := make([]byte, len(print))
	n, err := file.ReadAt(read, offset)
	if err != nil && err != io.EOF {
		return false
	}
	return n == len(print) && bytes.Equal(read, print)
}

type replayRecorder struct {
	start       int64
	startPrint  []byte
	last        int64
	checkpoints []replayCheckpoint
}

func newReplayRecorder() *replayRecorder {
	return &replayRecorder{start: -1}
}

// visit must see each record before the tracker observes it; it reports
// whether the record starts a turn as a turnStartSearch finds them.
func (r *replayRecorder) visit(tracker *claudeTurnTracker, record map[string]any, line []byte, offset int64) bool {
	if claudeSearchStartsTurn(record) && claudeLineMayStartTurn(line) {
		r.start, r.startPrint, r.last, r.checkpoints = offset, replayPrint(line), offset, nil
		return true
	}
	if r.start >= 0 && offset-r.last >= replayCheckpointSpacing {
		r.checkpoints = append(r.checkpoints, replayCheckpoint{offset: offset, print: replayPrint(line), tracker: tracker.checkpoint()})
		r.last = offset
	}
	return false
}

func replayPrint(line []byte) []byte {
	return bytes.Clone(line[:min(len(line), replayPrintBytes)])
}

// checkpoint copies the tracker, folding its meter's counted messages into
// one total apart from the message still being written.
func (t *claudeTurnTracker) checkpoint() *claudeTurnTracker {
	copied := *t
	copied.terminal = maps.Clone(t.terminal)
	copied.between = slices.Clone(t.between)
	copied.meter.compactions = slices.Clone(t.meter.compactions)
	copied.meter.seenBefore = nil
	if t.meter.context != nil {
		context := *t.meter.context
		copied.meter.context = &context
	}
	if t.meter.usage != nil {
		copied.meter.usage = map[string]surface.TurnUsage{}
		var total surface.TurnUsage
		var reasoning int64
		reasoningKnown, folded := true, false
		for id, counted := range t.meter.usage {
			if id == t.meter.lastUsage {
				copied.meter.usage[id] = counted
				continue
			}
			folded = true
			total.InputTokens += counted.InputTokens
			total.CachedInputTokens += counted.CachedInputTokens
			total.CacheWriteTokens += counted.CacheWriteTokens
			total.OutputTokens += counted.OutputTokens
			if counted.ReasoningTokens == nil {
				reasoningKnown = false
			} else {
				reasoning += *counted.ReasoningTokens
			}
		}
		if folded {
			if reasoningKnown {
				total.ReasoningTokens = &reasoning
			}
			copied.meter.usage[foldedUsageID] = total
		}
	}
	return &copied
}

// foldedUsageID keys a checkpoint's folded total; Claude message ids are
// never empty, and the meter skips usage without one.
const foldedUsageID = ""

func (t *claudeTurnTracker) sawInterleaving() bool {
	return t.interleaved || t.meter.interleaved
}

func (m *claudeTurnMeter) usageIDs(before map[string]bool) map[string]bool {
	ids := maps.Clone(before)
	if ids == nil {
		ids = map[string]bool{}
	}
	for id := range m.usage {
		if id != foldedUsageID {
			ids[id] = true
		}
	}
	return ids
}
