package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) readJournalPage(sessionID string, before uint64, limit int) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Source: "journal", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}}
	toItem := d.journalItemReader(sessionID)
	var collected []journalPageItem
	var items []surface.TimelineItem
	var positions, starts []int
	cursor := before
	var page registry.SessionJournalPage
	reachedStart := false
	collectedBytes := 0
	batch := min(200, max(16, 4*(limit+1)))
	for {
		next, err := d.Registry.ReadSessionJournalPage(sessionID, cursor, batch)
		var gap *registry.SessionJournalHistoryGapError
		if cursor != before && errors.As(err, &gap) {
			reachedStart = true
			break
		}
		if err != nil {
			return result, err
		}
		page = next
		if cursor == before {
			result.JournalSeq = page.LatestSeq
		}
		previousRows := len(collected)
		collected = slices.Grow(collected, len(page.Entries))
		collected = collected[:previousRows+len(page.Entries)]
		copy(collected[len(page.Entries):], collected[:previousRows])
		chunk := collected[:len(page.Entries)]
		for index, entry := range page.Entries {
			var payload sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				return result, fmt.Errorf("decode journal item: %w", err)
			}
			chunk[index] = journalPageItem{seq: entry.Seq, position: entry.Position, size: len(entry.Payload), payload: payload}
		}
		for _, item := range chunk {
			collectedBytes += item.size
		}
		chunkItems, chunkPositions := journalVisibleItems(chunk, toItem)
		for index := range positions {
			positions[index] += len(chunk)
		}
		if previousRows == 0 {
			items, positions = chunkItems, chunkPositions
		} else {
			items = slices.Insert(items, 0, chunkItems...)
			positions = slices.Insert(positions, 0, chunkPositions...)
		}
		starts = surface.ExchangeStarts(items)
		reachedStart = page.NextBefore == 0 || len(page.Entries) < batch
		if len(starts) > limit || len(collected) >= sessionPageScanRows || collectedBytes > sessionPageBytes || reachedStart {
			break
		}
		cursor = page.NextBefore
		if len(starts) < 2 {
			batch = 200 - len(collected)%200
		} else {
			batch = min(200-len(collected)%200, 2*batch)
		}
	}
	first, truncated := journalPageStart(collected, positions, starts, limit, reachedStart)
	result.Truncated = truncated
	if first > 0 {
		result.NextBefore = int64(collected[first].position)
	} else {
		result.NextBefore = int64(page.NextBefore)
		if page.HistoryBefore > 0 {
			result.NextBefore = encodeProviderHistoryCursor(page.HistoryBefore)
		}
	}
	result.Items = items[sort.SearchInts(positions, first):]
	var newestSourceErrorSeq uint64
	for _, entry := range collected[first:] {
		payload := entry.payload
		if payload.Kind == "source-error" {
			newestSourceErrorSeq = entry.seq
			result.UnavailableReason = payload.Reason
			continue
		}
		if !journalRowIsItem(payload) {
			continue
		}
		result.UnavailableReason = ""
		result.Truncated = result.Truncated || payload.Truncated
	}
	result.Exchanges = surface.ExchangesFromItems(result.Items, "journal")
	if status, seedSeq, _, statusErr := d.Registry.SessionJournalSeedCheckpoint(sessionID); statusErr == nil && status == registry.SessionJournalSeeded && (newestSourceErrorSeq == 0 || newestSourceErrorSeq <= seedSeq) {
		result.UnavailableReason = ""
	}
	return result, nil
}
