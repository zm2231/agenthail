package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const sessionPageItemLimit = 400

type journalPageItem struct {
	seq     uint64
	payload sessionJournalPayload
}

// readJournalPage returns the newest limit exchanges before the cursor, each from its prompt, with
// every item of those turns. A turn longer than the item limit starts mid-turn.
func (d *Daemon) readJournalPage(sessionID string, before uint64, limit int) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Source: "journal", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}}
	var collected []journalPageItem
	prompts := 0
	cursor := before
	var page registry.SessionJournalPage
	for {
		next, err := d.Registry.ReadSessionJournalPage(sessionID, cursor, 200)
		var gap *registry.SessionJournalHistoryGapError
		if cursor != before && errors.As(err, &gap) {
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
			chunk = append(chunk, journalPageItem{seq: entry.Seq, payload: payload})
			if journalPrompt(payload) {
				prompts++
			}
		}
		collected = append(chunk, collected...)
		if prompts >= limit || len(collected) >= sessionPageItemLimit || page.NextBefore == 0 || len(page.Entries) < 200 {
			break
		}
		cursor = page.NextBefore
	}
	start := 0
	for index, seen := len(collected)-1, 0; index >= 0; index-- {
		if journalPrompt(collected[index].payload) {
			seen++
			if seen == limit {
				start = index
				break
			}
		}
	}
	start = max(start, len(collected)-sessionPageItemLimit)
	if start > 0 {
		result.NextBefore = int64(collected[start].seq)
	} else {
		result.NextBefore = int64(page.NextBefore)
		if page.HistoryBefore > 0 {
			result.NextBefore = encodeProviderHistoryCursor(page.HistoryBefore)
		}
	}
	var newestSourceErrorSeq uint64
	for _, entry := range collected[start:] {
		payload := entry.payload
		if payload.Kind == "source-error" {
			newestSourceErrorSeq = entry.seq
			result.UnavailableReason = payload.Reason
			continue
		}
		if payload.Op == "remove" {
			continue
		}
		result.UnavailableReason = ""
		result.Items = append(result.Items, journalTimelineItem(payload))
		result.Truncated = result.Truncated || payload.Truncated
	}
	result.Exchanges = surface.ExchangesFromItems(result.Items, "journal")
	if status, seedSeq, _, statusErr := d.Registry.SessionJournalSeedCheckpoint(sessionID); statusErr == nil && status == registry.SessionJournalSeeded && (newestSourceErrorSeq == 0 || newestSourceErrorSeq <= seedSeq) {
		result.UnavailableReason = ""
	}
	return result, nil
}

func journalPrompt(payload sessionJournalPayload) bool {
	return payload.Op != "remove" && surface.IsPromptItem(journalTimelineItem(payload))
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
	return surface.TimelineItem{ID: payload.ItemID, Kind: payload.Kind, Role: role, Origin: payload.Origin, Sender: payload.Sender, Title: title, Text: payload.Body, Timestamp: payload.TS, CallID: callID, TurnID: payload.TurnID, Status: payload.Status, Truncated: payload.Truncated, TruncationReason: payload.TruncationReason, BodyRef: payload.BodyRef, Attachment: payload.Attachment}
}

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

func (d *Daemon) readProviderHistoryPage(ctx context.Context, session *surface.Session, adapter surface.Surface, before int64, limit int) (*surface.SessionReadResult, error) {
	var result *surface.SessionReadResult
	var items []surface.TimelineItem
	prompts := 0
	for cursor := before; ; {
		read, err := d.sources.readHistory(ctx, session, adapter, cursor, 40)
		if err != nil {
			if result != nil {
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
			if surface.IsPromptItem(item) {
				prompts++
			}
			page = append(page, item)
		}
		items = append(page, items...)
		if result == nil {
			copied := *read
			result = &copied
		}
		result.NextBefore = read.NextBefore
		result.Truncated = result.Truncated || read.Truncated
		if prompts >= limit || len(items) >= sessionPageItemLimit || read.NextBefore <= 0 || read.NextBefore >= cursor && cursor > 0 {
			break
		}
		cursor = read.NextBefore
	}
	result.Items = items
	result.Exchanges = surface.ExchangesFromItems(items, result.Source)
	result.NextBefore = encodeProviderHistoryCursor(result.NextBefore)
	return result, nil
}
