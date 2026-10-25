package daemon

import (
	"context"

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
// item: every row but a source's errors, removals, and the goal, context and
// commands state rows, which describe the session rather than an event in it.
func journalRowIsItem(payload sessionJournalPayload) bool {
	switch payload.Kind {
	case "source-error", "goal", "context", "commands":
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
	read := journalTimelineItem
	if kind, _, _, err := d.Registry.GetSession(sessionID); err == nil && kind == string(surface.KindCodex) {
		read = codexJournalItemReader(func(ref string) ([]byte, error) {
			body, _, err := d.Registry.SessionJournalBody(sessionID, ref, 0, sessionJournalRetentionBytes)
			return body, err
		})
	}
	return func(payload sessionJournalPayload) surface.TimelineItem {
		return surface.PresentTimelineItem(read(payload))
	}
}

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
	result.Items = surface.PresentTimelineItems(items)
	result.Exchanges = surface.ExchangesFromItems(result.Items, result.Source)
	result.NextBefore = encodeProviderHistoryCursor(result.NextBefore)
	return result, nil
}
