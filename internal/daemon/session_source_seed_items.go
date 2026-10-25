package daemon

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (s *sessionSource) appendSeedItems(items []surface.TimelineItem) {
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		event := seedEvent(s.session.Surface, item)
		if s.mergeSeedIntoAuthoritativeRow(event.ProviderKey, event.Operation, item) {
			continue
		}
		s.append(event)
	}
}

func seedEvent(kind surface.SurfaceKind, item surface.TimelineItem) surface.StreamEvent {
	at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
	providerKey := "timeline:" + item.ID
	if kind == surface.KindCodex && strings.HasPrefix(item.ID, "codex:") {
		providerKey = item.ID
	}
	operation := "upsert"
	if item.Kind == "done" {
		operation = "phase"
	}
	return surface.StreamEvent{
		Role:             item.Role,
		Origin:           item.Origin,
		Sender:           item.Sender,
		Title:            item.Title,
		Status:           item.Status,
		ResetsAt:         item.ResetsAt,
		Truncated:        item.Truncated,
		TruncationReason: item.TruncationReason,
		ID:               item.ID,
		ProviderKey:      providerKey,
		Version:          uint64(len(item.Text)),
		Operation:        operation,
		TurnID:           item.TurnID,
		MessageID:        item.MessageID,
		CallID:           item.CallID,
		Attachment:       item.Attachment,
		Files:            item.Files,
		Timestamp:        at,
		Kind:             item.Kind,
		Text:             item.Text,
		Turn:             item.Turn,
	}
}

func (s *sessionSource) mergeSeedIntoAuthoritativeRow(providerKey, operation string, item surface.TimelineItem) bool {
	journalKey := providerKey
	if operation == "phase" {
		journalKey += ":phase:" + item.Kind
	}
	entry, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, journalKey)
	if err != nil || !found {
		return false
	}
	var payload sessionJournalPayload
	if json.Unmarshal(entry.Payload, &payload) != nil || payload.Kind != item.Kind {
		return false
	}
	body := payload.Body
	if payload.BodyRef != "" {
		if len(entry.FullBody) == 0 {
			return false
		}
		body = string(entry.FullBody)
	}
	if body != item.Text && (!item.Truncated || !strings.HasPrefix(body, item.Text)) {
		return false
	}
	if body != "" && item.Kind != "done" {
		s.mu.Lock()
		s.appendBodies[providerKey] = body
		s.mu.Unlock()
	}
	merged := payload
	mergeSeedField(&merged.Role, item.Role)
	mergeSeedField(&merged.Origin, item.Origin)
	mergeSeedField(&merged.Sender, item.Sender)
	mergeSeedField(&merged.Title, item.Title)
	mergeSeedField(&merged.Status, item.Status)
	mergeSeedField(&merged.TurnID, item.TurnID)
	mergeSeedField(&merged.MessageID, item.MessageID)
	mergeSeedField(&merged.CallID, item.CallID)
	if body == item.Text && item.Truncated {
		merged.Truncated = true
		if merged.TruncationReason == "" {
			merged.TruncationReason = item.TruncationReason
		}
	}
	if item.Attachment != nil {
		merged.Attachment = item.Attachment
	}
	if len(item.Files) > 0 {
		merged.Files = item.Files
	}
	if item.Turn != nil {
		merged.Turn = item.Turn
	}
	if merged.Role == payload.Role && merged.Origin == payload.Origin && merged.Sender == payload.Sender && merged.Title == payload.Title && merged.Status == payload.Status && merged.TurnID == payload.TurnID && merged.MessageID == payload.MessageID && merged.CallID == payload.CallID && merged.Truncated == payload.Truncated && merged.TruncationReason == payload.TruncationReason && reflect.DeepEqual(merged.Attachment, payload.Attachment) && reflect.DeepEqual(merged.Files, payload.Files) && reflect.DeepEqual(merged.Turn, payload.Turn) {
		return true
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return true
	}
	_, changed, err := s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: merged.Kind, ProviderKey: journalKey, Payload: encoded, BodyRef: payload.BodyRef, FullBody: entry.FullBody, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		s.mu.Lock()
		s.appendFailed = true
		s.mu.Unlock()
		s.appendSourceError(fmt.Errorf("session journal append failed: %w", err))
	} else if changed {
		s.wakeFollowers()
	}
	return true
}

func mergeSeedField(target *string, value string) {
	if value != "" {
		*target = value
	}
}
