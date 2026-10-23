package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	// sessionPageScanRows bounds the journal rows one page reads while
	// looking for the start of its oldest exchange: the whole retained
	// journal of a session.
	sessionPageScanRows = sessionJournalRetentionCount
	// sessionPageBytes bounds the journal payload one page returns, so the
	// page with its exchanges stays inside what a client reads.
	sessionPageBytes = 1 << 20
)

type journalPageItem struct {
	seq      uint64
	position uint64
	size     int
	payload  sessionJournalPayload
}

// readJournalPage returns the newest limit exchanges before the cursor, each from the item that
// opened it, with every item of those turns. It reads until it has seen where the oldest of them
// starts or the journal begins, at most sessionPageScanRows rows and a little past sessionPageBytes,
// and returns the whole exchanges that fit sessionPageBytes; the cursor addresses the next one.
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
	for {
		next, err := d.Registry.ReadSessionJournalPage(sessionID, cursor, 200)
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
		chunk := make([]journalPageItem, 0, len(page.Entries))
		for _, entry := range page.Entries {
			var payload sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				return result, fmt.Errorf("decode journal item: %w", err)
			}
			chunk = append(chunk, journalPageItem{seq: entry.Seq, position: entry.Position, size: len(entry.Payload), payload: payload})
		}
		collected = append(chunk, collected...)
		for _, item := range chunk {
			collectedBytes += item.size
		}
		items, positions = journalVisibleItems(collected, toItem)
		starts = surface.ExchangeStarts(items)
		reachedStart = page.NextBefore == 0 || len(page.Entries) < 200
		if len(starts) > limit || len(collected) >= sessionPageScanRows || collectedBytes > sessionPageBytes || reachedStart {
			break
		}
		cursor = page.NextBefore
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
		result.Items = append(result.Items, toItem(payload))
		result.Truncated = result.Truncated || payload.Truncated
	}
	result.Exchanges = surface.ExchangesFromItems(result.Items, "journal")
	if status, seedSeq, _, statusErr := d.Registry.SessionJournalSeedCheckpoint(sessionID); statusErr == nil && status == registry.SessionJournalSeeded && (newestSourceErrorSeq == 0 || newestSourceErrorSeq <= seedSeq) {
		result.UnavailableReason = ""
	}
	return result, nil
}

// journalPageStart returns the first collected row of the page: the start of
// the oldest of at most limit exchanges that fit sessionPageBytes, or the
// journal's first row when the page reaches it. A newest exchange larger than
// a page starts mid-turn and is reported truncated.
func journalPageStart(collected []journalPageItem, positions, starts []int, limit int, reachedStart bool) (int, bool) {
	suffix := make([]int, len(collected)+1)
	for index := len(collected) - 1; index >= 0; index-- {
		suffix[index] = suffix[index+1] + collected[index].size
	}
	if reachedStart && len(starts) <= limit && suffix[0] <= sessionPageBytes {
		return 0, false
	}
	for _, start := range starts[max(0, len(starts)-limit):] {
		if suffix[positions[start]] <= sessionPageBytes {
			return positions[start], false
		}
	}
	first := len(collected)
	for first > 0 && suffix[first-1] <= sessionPageBytes {
		first--
	}
	return min(first, max(0, len(collected)-1)), true
}

// journalRowIsItem reports whether a page shows a journal row as a timeline
// item: every row but a source's errors, removals, and the goal and context
// state rows, which describe the session rather than an event in it.
func journalRowIsItem(payload sessionJournalPayload) bool {
	switch payload.Kind {
	case "source-error", "goal", "context":
		return false
	}
	return payload.Op != "remove"
}

// journalVisibleItems returns the timeline items among entries and each item's entry position.
func journalVisibleItems(entries []journalPageItem, toItem func(sessionJournalPayload) surface.TimelineItem) ([]surface.TimelineItem, []int) {
	items := make([]surface.TimelineItem, 0, len(entries))
	positions := make([]int, 0, len(entries))
	for index, entry := range entries {
		if !journalRowIsItem(entry.payload) {
			continue
		}
		items = append(items, toItem(entry.payload))
		positions = append(positions, index)
	}
	return items, positions
}

// journalItemReader returns how a session's journal rows read as timeline
// items. A Codex user row journaled before injected context was told apart
// from prompts reads as instructions when its whole body is Codex's injected
// context; a row whose full body was not retained stays a prompt, since the
// request Codex appends may be in the part that was dropped.
func (d *Daemon) journalItemReader(sessionID string) func(sessionJournalPayload) surface.TimelineItem {
	kind, _, _, err := d.Registry.GetSession(sessionID)
	if err != nil || kind != string(surface.KindCodex) {
		return journalTimelineItem
	}
	return codexJournalItemReader(func(ref string) ([]byte, error) {
		body, _, err := d.Registry.SessionJournalBody(sessionID, ref, 0, sessionJournalRetentionBytes)
		return body, err
	})
}

// codexJournalItemReader classifies Codex journal rows, reading each
// retained body at most once: a page classifies its rows more than once
// while it looks for where its oldest exchange starts.
func codexJournalItemReader(readBody func(ref string) ([]byte, error)) func(sessionJournalPayload) surface.TimelineItem {
	injected := map[string]bool{}
	return func(payload sessionJournalPayload) surface.TimelineItem {
		item := journalTimelineItem(payload)
		if item.Kind != "message" || item.Role != "user" {
			return item
		}
		isContext := surface.IsCodexInjectedContext(payload.Body)
		if payload.Truncated {
			if payload.BodyRef == "" {
				return item
			}
			known, found := injected[payload.BodyRef]
			if !found {
				full, err := readBody(payload.BodyRef)
				known = err == nil && surface.IsCodexInjectedContext(string(full))
				injected[payload.BodyRef] = known
			}
			isContext = known
		}
		if isContext {
			item.Kind, item.Role, item.Title = "instructions", "", "Instructions"
		}
		return item
	}
}

func journalTimelineItem(payload sessionJournalPayload) surface.TimelineItem {
	title := payload.Title
	if title == "" {
		title = payload.Kind
	}
	role := payload.Role
	if role == "" && (payload.Kind == "text" || payload.Kind == "assistant") {
		role = "assistant"
	}
	callID := payload.CallID
	if callID == "" {
		callID = payload.TurnID
	}
	return surface.TimelineItem{ID: payload.ItemID, Kind: payload.Kind, Role: role, Origin: payload.Origin, Sender: payload.Sender, Title: title, Text: payload.Body, Timestamp: payload.TS, CallID: callID, TurnID: payload.TurnID, Status: payload.Status, ResetsAt: payload.ResetsAt, Truncated: payload.Truncated, TruncationReason: payload.TruncationReason, BodyRef: payload.BodyRef, Attachment: payload.Attachment, Files: payload.Files, Turn: payload.Turn}
}

// providerHistoryItemLimit bounds the items one page of provider history
// gathers; provider pages are whole exchanges, so stopping between them never
// splits a turn.
const providerHistoryItemLimit = 400

const providerHistoryCursorBase = int64(1) << 52

func encodeProviderHistoryCursor(before int64) int64 {
	if before <= 0 || before >= providerHistoryCursorBase {
		return 0
	}
	return providerHistoryCursorBase + before
}

func decodeProviderHistoryCursor(cursor int64) (int64, bool) {
	if cursor <= providerHistoryCursorBase {
		return 0, false
	}
	return cursor - providerHistoryCursorBase, true
}

// readProviderHistoryPage reads whole provider pages, newest first, until they hold limit
// exchanges or the next page would pass the item limit; that page is left for the next cursor.
func (d *Daemon) readProviderHistoryPage(ctx context.Context, session *surface.Session, adapter surface.Surface, before int64, limit int) (*surface.SessionReadResult, error) {
	var result *surface.SessionReadResult
	var items []surface.TimelineItem
	for cursor := before; ; {
		read, err := d.sources.readHistory(ctx, session, adapter, cursor, 40)
		if err != nil {
			if result != nil {
				result.NextBefore = cursor
				break
			}
			return &surface.SessionReadResult{Source: "history", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, UnavailableReason: boundedSessionSourceReason("Older activity could not be read: " + err.Error())}, nil
		}
		page := make([]surface.TimelineItem, 0, len(read.Items))
		for _, item := range read.Items {
			// An injected prompt's queued and delivered copies share an ID; the
			// newer copy may already be in the journal the client has paged.
			if item.Origin != "" {
				if _, found, err := d.Registry.SessionJournalEntryByProviderKey(session.ID, "timeline:"+item.ID); err == nil && found {
					continue
				}
			}
			page = append(page, item)
		}
		if result != nil && len(items)+len(page) > providerHistoryItemLimit {
			result.NextBefore = cursor
			break
		}
		items = append(page, items...)
		if result == nil {
			copied := *read
			result = &copied
		}
		result.NextBefore = read.NextBefore
		result.Truncated = result.Truncated || read.Truncated
		if len(surface.ExchangeStarts(items)) >= limit || read.NextBefore <= 0 || (cursor > 0 && read.NextBefore >= cursor) {
			break
		}
		cursor = read.NextBefore
	}
	result.Items = items
	result.Exchanges = surface.ExchangesFromItems(items, result.Source)
	result.NextBefore = encodeProviderHistoryCursor(result.NextBefore)
	return result, nil
}
