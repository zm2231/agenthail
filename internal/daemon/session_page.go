package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) readJournalPage(sessionID string, before uint64, limit int) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Source: "journal", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}}
	page, err := d.Registry.ReadSessionJournalPage(sessionID, before, limit)
	if err != nil {
		return result, err
	}
	result.NextBefore = int64(page.NextBefore)
	result.JournalSeq = page.LatestSeq
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return result, fmt.Errorf("decode journal item: %w", err)
		}
		if payload.Kind == "source-error" {
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
		result.Items = append(result.Items, surface.TimelineItem{ID: payload.ItemID, Kind: payload.Kind, Role: role, Title: title, Text: payload.Body, Timestamp: payload.TS, CallID: payload.TurnID, Status: payload.Status, Truncated: payload.Truncated, TruncationReason: payload.TruncationReason, BodyRef: payload.BodyRef})
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
	return result, nil
}
