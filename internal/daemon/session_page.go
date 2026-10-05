package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) readJournalPage(sessionID string, before uint64, limit int) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Source: "journal", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}}
	page, err := d.Registry.ReadSessionJournalPage(sessionID, before, limit)
	if err != nil {
		return result, err
	}
	result.NextBefore = int64(page.NextBefore)
	if page.HistoryBefore > 0 {
		result.NextBefore = encodeProviderHistoryCursor(page.HistoryBefore)
	}
	result.JournalSeq = page.LatestSeq
	var newestSourceErrorSeq uint64
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return result, fmt.Errorf("decode journal item: %w", err)
		}
		if payload.Kind == "source-error" {
			newestSourceErrorSeq = entry.Seq
			result.UnavailableReason = payload.Reason
			continue
		}
		if payload.Op == "remove" {
			continue
		}
		result.UnavailableReason = ""
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
		result.Items = append(result.Items, surface.TimelineItem{ID: payload.ItemID, Kind: payload.Kind, Role: role, Origin: payload.Origin, Sender: payload.Sender, Title: title, Text: payload.Body, Timestamp: payload.TS, CallID: callID, TurnID: payload.TurnID, Status: payload.Status, Truncated: payload.Truncated, TruncationReason: payload.TruncationReason, BodyRef: payload.BodyRef, Attachment: payload.Attachment})
		result.Truncated = result.Truncated || payload.Truncated
		if payload.Kind != "message" && payload.Kind != "text" && payload.Kind != "assistant" {
			continue
		}
		if role == "user" {
			result.Exchanges = append(result.Exchanges, surface.Exchange{User: payload.Body, Source: "journal"})
		} else if role == "assistant" {
			if len(result.Exchanges) == 0 || result.Exchanges[len(result.Exchanges)-1].Assistant != "" {
				result.Exchanges = append(result.Exchanges, surface.Exchange{Source: "journal"})
			}
			result.Exchanges[len(result.Exchanges)-1].Assistant = payload.Body
		}
	}
	if status, seedSeq, _, statusErr := d.Registry.SessionJournalSeedCheckpoint(sessionID); statusErr == nil && status == registry.SessionJournalSeeded && (newestSourceErrorSeq == 0 || newestSourceErrorSeq <= seedSeq) {
		result.UnavailableReason = ""
	}
	return result, nil
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
	read, err := d.sources.readHistory(ctx, session, adapter, before, limit)
	if err != nil {
		return &surface.SessionReadResult{Source: "history", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, UnavailableReason: boundedSessionSourceReason("Older activity could not be read: " + err.Error())}, nil
	}
	result := *read
	items := make([]surface.TimelineItem, 0, len(read.Items))
	for _, item := range read.Items {
		// An injected prompt's queued and delivered copies share an ID; the
		// newer copy may already be in the journal the client has paged.
		if item.Origin != "" {
			if _, found, err := d.Registry.SessionJournalEntryByProviderKey(session.ID, "timeline:"+item.ID); err == nil && found {
				continue
			}
		}
		items = append(items, item)
	}
	result.Items = items
	if result.Exchanges == nil {
		result.Exchanges = []surface.Exchange{}
	}
	result.NextBefore = encodeProviderHistoryCursor(read.NextBefore)
	return &result, nil
}
