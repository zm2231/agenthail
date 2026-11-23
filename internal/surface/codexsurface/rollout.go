package codexsurface

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

// Codex's thread/revert keeps a thread's id but continues it in a new rollout
// file, rollout-<ts>-<thread>_<rollout>.jsonl. The new file holds only its own
// records; its session_meta.history_base points at the file it continues and
// the byte offset where that file's retained history ends. Everything the
// earlier file holds past that offset was reverted.

const (
	codexRolloutLineageMax = 16
	codexRolloutCacheAge   = 24 * time.Hour
	codexRolloutCacheBytes = int64(2 << 30)
)

var errCodexRolloutLineage = errors.New("Codex rollout continuation could not be verified")

type codexRolloutHeader struct {
	Type    string `json:"type"`
	Payload struct {
		ID          string `json:"id"`
		HistoryBase *struct {
			RolloutID     string `json:"thread_id"`
			EndByteOffset int64  `json:"end_byte_offset"`
		} `json:"history_base"`
	} `json:"payload"`
}

// A segment is bytes [start, end) of path, the readable copy of source.
type codexRolloutSegment struct {
	source string
	path   string
	start  int64
	end    int64
}

func readCodexRolloutHeader(path string) (codexRolloutHeader, int64, error) {
	var header codexRolloutHeader
	end, err := transcript.ReadFirstJSONL(path, &header)
	if err != nil {
		return header, 0, fmt.Errorf("read Codex rollout header: %w", err)
	}
	if header.Type != "session_meta" {
		return header, 0, fmt.Errorf("Codex rollout has no session header")
	}
	return header, end, nil
}

// The segments' copies are held until release is called; on an error
// nothing is held.
func (h CodexHome) rolloutLineage(current, threadID string) (segments []codexRolloutSegment, release func(), err error) {
	var releases []func()
	releaseAll := func() {
		for _, held := range releases {
			held()
		}
	}
	defer func() {
		if err != nil {
			releaseAll()
		}
	}()
	source, end := current, int64(-1)
	for {
		if len(segments) == codexRolloutLineageMax {
			return nil, nil, fmt.Errorf("%w: more than %d rollouts", errCodexRolloutLineage, codexRolloutLineageMax)
		}
		path, held, err := codexReadableRollout(source)
		if err != nil {
			return nil, nil, err
		}
		releases = append(releases, held)
		header, length, err := readCodexRolloutHeader(path)
		if err != nil {
			return nil, nil, err
		}
		if header.Payload.ID != threadID {
			return nil, nil, fmt.Errorf("%w: %s belongs to another thread", errCodexRolloutLineage, filepath.Base(source))
		}
		base := header.Payload.HistoryBase
		start := int64(0)
		if base != nil {
			start = length
		}
		if end >= 0 {
			info, err := os.Stat(path)
			if err != nil {
				return nil, nil, err
			}
			if end < start || end > info.Size() {
				return nil, nil, fmt.Errorf("%w: history ends outside %s", errCodexRolloutLineage, filepath.Base(source))
			}
		}
		segments = append(segments, codexRolloutSegment{source: source, path: path, start: start, end: end})
		if base == nil {
			break
		}
		previous, err := h.findRollout(base.RolloutID, threadID)
		if err != nil {
			return nil, nil, err
		}
		source, end = previous, base.EndByteOffset
	}
	for left, right := 0, len(segments)-1; left < right; left, right = left+1, right-1 {
		segments[left], segments[right] = segments[right], segments[left]
	}
	return segments, releaseAll, nil
}

// Rollout ids are UUIDv7, so a rollout's id dates its folder.
func (h CodexHome) findRollout(rolloutID, threadID string) (string, error) {
	name := threadID
	if rolloutID != threadID {
		name = threadID + "_" + rolloutID
	}
	var matches []string
	for _, day := range codexRolloutDays(uuidV7Time(rolloutID)) {
		for _, suffix := range []string{".jsonl", ".jsonl.zst"} {
			found, _ := filepath.Glob(filepath.Join(h.sessionsDir(), day, "rollout-*-"+name+suffix))
			matches = append(matches, found...)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("%w: found %d rollouts for %s", errCodexRolloutLineage, len(matches), rolloutID)
	}
	return matches[0], nil
}

// Codex names a rollout's folder by local date, which can differ from UTC by
// a day either way.
func codexRolloutDays(at time.Time) []string {
	if at.IsZero() {
		return nil
	}
	dates := []time.Time{at.In(time.Local), at}
	for _, base := range append([]time.Time(nil), dates...) {
		dates = append(dates, base.AddDate(0, 0, -1), base.AddDate(0, 0, 1))
	}
	seen := map[string]bool{}
	var days []string
	for _, date := range dates {
		day := filepath.Join(date.Format("2006"), date.Format("01"), date.Format("02"))
		if !seen[day] {
			seen[day] = true
			days = append(days, day)
		}
	}
	return days
}

// Codex compresses rollouts untouched for a week. history_base offsets
// address the decompressed bytes, so a compressed rollout is read from a
// decompressed copy. The copy is held until release is called, and eviction
// never removes a held copy.
func codexReadableRollout(source string) (string, func(), error) {
	if !strings.HasSuffix(source, ".zst") {
		return source, func() {}, nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", nil, err
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(cacheRoot, "agenthail", "codex-rollouts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	key := sha256.Sum256([]byte(source + "\x00" + surface.Stamp(info).String()))
	path := filepath.Join(dir, hex.EncodeToString(key[:16])+".jsonl")
	release := codexRolloutCopies.hold(path)
	if _, err := os.Stat(path); err == nil {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return path, release, nil
	}
	if err := decompressCodexRollout(source, path); err != nil {
		release()
		return "", nil, err
	}
	return path, release, nil
}

func decompressCodexRollout(source, path string) error {
	compressed, err := os.Open(source)
	if err != nil {
		return err
	}
	defer compressed.Close()
	decoder, err := zstd.NewReader(compressed)
	if err != nil {
		return err
	}
	defer decoder.Close()
	if err := atomicfile.WriteFrom(path, decoder, 0600); err != nil {
		return fmt.Errorf("decompress Codex rollout: %w", err)
	}
	return nil
}

type codexRolloutStream struct {
	*zstd.Decoder
	file *os.File
}

func (s codexRolloutStream) Close() error {
	s.Decoder.Close()
	return s.file.Close()
}

func openCodexRollout(source string) (io.ReadCloser, error) {
	file, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(source, ".zst") {
		return file, nil
	}
	decoder, err := zstd.NewReader(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return codexRolloutStream{Decoder: decoder, file: file}, nil
}

func readCodexRolloutFirstLine(source string, value any) error {
	stream, err := openCodexRollout(source)
	if err != nil {
		return err
	}
	defer stream.Close()
	_, err = transcript.DecodeFirstJSONL(stream, value)
	return err
}

type codexRolloutCopySet struct {
	mu   sync.Mutex
	held map[string]int
}

var codexRolloutCopies = &codexRolloutCopySet{held: map[string]int{}}

// Releasing the last hold prunes the cache, so a copy that pushed it past
// the cap goes once no read needs it.
func (s *codexRolloutCopySet) hold(path string) func() {
	s.mu.Lock()
	s.held[path]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.held[path]--; s.held[path] <= 0 {
				delete(s.held, path)
			}
			s.mu.Unlock()
			s.prune(filepath.Dir(path))
		})
	}
}

// Copies are touched on use, so the oldest modification time is the least
// recently used. A held copy is never removed: the cache exceeds its cap
// only by copies open reads need. A temporary file another write is still
// filling starts with a dot and is left to the age limit.
func (s *codexRolloutCopySet) prune(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type cached struct {
		path string
		size int64
		used time.Time
	}
	var copies []cached
	total := int64(0)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if s.held[path] > 0 {
			total += info.Size()
			continue
		}
		if time.Since(info.ModTime()) > codexRolloutCacheAge {
			_ = os.Remove(path)
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") || !info.Mode().IsRegular() {
			continue
		}
		copies = append(copies, cached{path: path, size: info.Size(), used: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].used.Before(copies[j].used) })
	for _, old := range copies {
		if total <= codexRolloutCacheBytes {
			return
		}
		if os.Remove(old.path) == nil {
			total -= old.size
		}
	}
}

// Cursors address the rollouts' retained bytes laid end to end, so a thread
// that never continued pages by its own file's offsets. Identity and offset
// are always the current rollout's: a stream follows only that one.
func readCodexTranscriptPage(ctx context.Context, home CodexHome, path, threadID string, before int64, limit int) (*surface.SessionReadResult, error) {
	if path == "" {
		return transcript.ReadPage(ctx, path, codexTranscript{}, before, limit)
	}
	header, _, err := readCodexRolloutHeader(path)
	if err != nil || header.Payload.HistoryBase == nil {
		return transcript.ReadPage(ctx, path, codexTranscript{}, before, limit)
	}
	segments, release, err := home.rolloutLineage(path, threadID)
	if err != nil {
		page, pageErr := transcript.ReadPage(ctx, path, codexTranscript{}, before, limit)
		if pageErr != nil {
			return nil, pageErr
		}
		page.Warning = "Activity from before this conversation was rewound is unavailable."
		return page, nil
	}
	defer release()
	last := len(segments) - 1
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	segments[last].end = info.Size()
	bases := make([]int64, len(segments))
	for index := 1; index < len(segments); index++ {
		bases[index] = bases[index-1] + segments[index-1].end - segments[index-1].start
	}
	index, local := last, int64(0)
	if before > 0 {
		index = -1
		for candidate := last; candidate >= 0; candidate-- {
			if before > bases[candidate] {
				index = candidate
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("activity transcript changed; refresh the session")
		}
		local = segments[index].start + before - bases[index]
		if local > segments[index].end {
			return nil, fmt.Errorf("activity transcript changed; refresh the session")
		}
	}
	first := index
	var result *surface.SessionReadResult
	for {
		segment := segments[index]
		remaining := limit
		if result != nil && limit > 0 {
			remaining = limit - len(result.Exchanges)
		}
		page := &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Source: "local-transcript"}
		if segment.end > segment.start {
			if local == 0 && index != last {
				local = segment.end
			}
			page, err = transcript.ReadWindow(ctx, segment.path, codexTranscript{}, segment.start, local, remaining)
			if err != nil {
				return nil, err
			}
		}
		exhausted := page.NextBefore <= segment.start
		switch {
		case !exhausted:
			page.NextBefore = bases[index] + page.NextBefore - segment.start
		case index > 0:
			page.NextBefore = bases[index]
		default:
			page.NextBefore = 0
		}
		if result == nil {
			result = page
		} else {
			result.Items = append(page.Items, result.Items...)
			result.Exchanges = append(page.Exchanges, result.Exchanges...)
			result.Truncated = result.Truncated || page.Truncated
			result.NextBefore = page.NextBefore
		}
		if !exhausted || index == 0 || limit > 0 && len(result.Exchanges) >= limit {
			break
		}
		index, local = index-1, 0
	}
	if first != last {
		current, err := transcript.ReadPage(ctx, path, codexTranscript{}, 0, 1)
		if err != nil {
			return nil, err
		}
		result.TranscriptOffset = current.TranscriptOffset
		result.TranscriptOffsetSet = current.TranscriptOffsetSet
		result.TranscriptIdentity = current.TranscriptIdentity
		result.CodexPendingEventUser = current.CodexPendingEventUser
		result.CodexPendingEventTurn = current.CodexPendingEventTurn
		result.CodexCurrentTurnID = current.CodexCurrentTurnID
	}
	return result, nil
}

// A trusted rollout Codex compressed or rewrote since is matched by path.
func (c *Codex) TranscriptContinuation(ctx context.Context, session *surface.Session, trustedPath, trustedIdentity string) ([]surface.TimelineItem, bool, error) {
	current := c.home.rolloutFile(session)
	if current == "" || current == trustedPath {
		return nil, false, nil
	}
	if trustedPath != "" && current == trustedPath+".zst" {
		return nil, true, nil
	}
	segments, release, err := c.home.rolloutLineage(current, session.ID)
	if err != nil {
		return nil, false, nil
	}
	defer release()
	for index := len(segments) - 2; index >= 0; index-- {
		segment := segments[index]
		matched := trustedPath != "" && (segment.source == trustedPath || segment.source == trustedPath+".zst")
		if !matched && trustedIdentity != "" {
			if info, err := os.Stat(segment.source); err == nil {
				matched = transcript.FileIdentity(info) == trustedIdentity
			}
		}
		if !matched {
			continue
		}
		reverted, err := codexRevertedItems(ctx, segment)
		if err != nil {
			return nil, false, err
		}
		return reverted, true, nil
	}
	return nil, false, nil
}

func codexRevertedItems(ctx context.Context, segment codexRolloutSegment) ([]surface.TimelineItem, error) {
	info, err := os.Stat(segment.path)
	if err != nil {
		return nil, err
	}
	var items []surface.TimelineItem
	for before := int64(0); ; {
		if before == 0 && info.Size() <= segment.end {
			return nil, nil
		}
		page, err := transcript.ReadWindow(ctx, segment.path, codexTranscript{}, segment.end, before, 0)
		if err != nil {
			return nil, err
		}
		items = append(page.Items, items...)
		if page.NextBefore <= segment.end {
			return items, nil
		}
		before = page.NextBefore
	}
}
