package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (s *sessionSource) restartMovedJournal(read *surface.SessionReadResult) error {
	notice := surface.TimelineItem{
		ID:        "transcript-restart:" + read.TranscriptIdentity,
		Kind:      "notice",
		Title:     "Conversation reloaded",
		Text:      "This conversation moved to a new transcript that could not be matched to the one shown before, so it was read again from the new transcript.",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	return s.restartJournal(read, append(slices.Clone(read.Items), notice))
}

func (s *sessionSource) restartJournal(read *surface.SessionReadResult, items []surface.TimelineItem) error {
	var fresh []surface.StreamEvent
	keep := map[string]bool{}
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		event := seedEvent(s.session.Surface, item)
		keep[event.ProviderKey] = true
		fresh = append(fresh, event)
	}
	var events []surface.StreamEvent
	for before := uint64(0); ; {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		var gap *registry.SessionJournalHistoryGapError
		if errors.As(err, &gap) {
			break
		}
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			var payload sessionJournalPayload
			if json.Unmarshal(entry.Payload, &payload) != nil || payload.Op == "remove" || payload.Kind == "source-error" || payload.ProviderKey == "" || keep[payload.ProviderKey] {
				continue
			}
			keep[payload.ProviderKey] = true
			events = append(events, surface.StreamEvent{ID: payload.ItemID, ProviderKey: payload.ProviderKey, Operation: "remove", Kind: payload.Kind, TurnID: payload.TurnID, Final: true})
		}
		if page.NextBefore == 0 {
			break
		}
		before = page.NextBefore
	}
	events = append(events, fresh...)
	entries := make([]registry.SessionJournalEntry, 0, len(events))
	bodies := map[string]string{}
	for _, event := range events {
		s.mu.Lock()
		payload, bodyKey := s.normalizeLocked(event)
		s.mu.Unlock()
		entry, err := sessionJournalEntry(s.session.ID, payload)
		if err != nil {
			return err
		}
		if bodyKey != "" {
			bodies[bodyKey] = payload.Body
		}
		entries = append(entries, entry)
	}
	derivation, err := s.manager.registry.SessionJournalDerivation(s.session.ID)
	if err != nil {
		return err
	}
	restarted, err := s.manager.registry.RestartSessionJournal(s.session.ID, entries, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}, derivation, max(0, read.NextBefore))
	if err != nil {
		return err
	}
	s.session.TranscriptOffset = read.TranscriptOffset
	s.session.TranscriptOffsetSet = read.TranscriptOffsetSet
	s.session.CodexPendingEventUser = read.CodexPendingEventUser
	s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
	s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
	s.session.CodexToolWindow = read.CodexToolWindow
	s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
	s.session.TranscriptIdentity = read.TranscriptIdentity
	s.mu.Lock()
	for key, body := range bodies {
		s.appendBodies[key] = body
	}
	s.mu.Unlock()
	if len(restarted) > 0 {
		s.wakeFollowers()
	}
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.TranscriptIdentity); err != nil {
		return err
	}
	return s.recordTranscriptCheckpoint(read.TranscriptOffset, read.TranscriptIdentity)
}

const transcriptJournalDerivation = 1

func (s *sessionSource) rederiveJournal(ctx context.Context) error {
	reader, ok := s.adapter.(surface.TranscriptItemReader)
	if !ok {
		return s.rederiveCodexJournal(ctx)
	}
	version, err := s.manager.registry.SessionJournalDerivation(s.session.ID)
	if err != nil || version >= transcriptJournalDerivation {
		return err
	}
	var rows []registry.SessionJournalEntry
	before := uint64(0)
	for {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		var gap *registry.SessionJournalHistoryGapError
		if errors.As(err, &gap) {
			break
		}
		if err != nil {
			return err
		}
		rows = append(page.Entries, rows...)
		if page.NextBefore == 0 {
			break
		}
		before = page.NextBefore
	}
	from := int64(-1)
	for _, row := range rows {
		if offset, ok := transcriptItemOffset(transcriptRowItemID(row)); ok && (from < 0 || offset < from) {
			from = offset
		}
	}
	if from < 0 {
		return s.manager.registry.MarkSessionJournalDerivation(s.session.ID, transcriptJournalDerivation)
	}
	s.providerMu.Lock()
	read, err := reader.TranscriptItems(ctx, &s.session, surface.TranscriptItemsRequest{From: from})
	s.providerMu.Unlock()
	if err != nil {
		return err
	}
	items := read.Items
	derived := make(map[string]bool, len(items))
	for _, item := range items {
		derived[item.ID] = true
	}
	var leading []registry.SessionJournalEntry
	following := map[string][]registry.SessionJournalEntry{}
	last := ""
	for _, row := range rows {
		if id := transcriptRowItemID(row); id != "" {
			if derived[id] {
				last = id
			}
			continue
		}
		if row.BodyRef != "" {
			full, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, row.ProviderKey)
			if err != nil {
				return err
			}
			if found {
				row.FullBody = full.FullBody
			}
		}
		if last == "" {
			leading = append(leading, row)
		} else {
			following[last] = append(following[last], row)
		}
	}
	entries := leading
	bodies := map[string]string{}
	for _, item := range items {
		s.mu.Lock()
		payload, bodyKey := s.normalizeLocked(seedEvent(s.session.Surface, item))
		s.mu.Unlock()
		entry, err := sessionJournalEntry(s.session.ID, payload)
		if err != nil {
			return err
		}
		if bodyKey != "" {
			bodies[bodyKey] = item.Text
		}
		entries = append(entries, entry)
		entries = append(entries, following[item.ID]...)
	}
	replaced, err := s.manager.registry.ReplaceSessionJournal(s.session.ID, entries, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}, transcriptJournalDerivation)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for key, body := range bodies {
		s.appendBodies[key] = body
	}
	s.mu.Unlock()
	if len(replaced) > 0 {
		s.wakeFollowers()
	}
	return nil
}

func transcriptRowItemID(row registry.SessionJournalEntry) string {
	id, found := strings.CutPrefix(row.ProviderKey, "timeline:")
	if !found {
		return ""
	}
	return strings.TrimSuffix(id, ":phase:"+row.Kind)
}

func transcriptItemOffset(itemID string) (int64, bool) {
	digits, _, found := strings.Cut(itemID, "-")
	if !found {
		return 0, false
	}
	offset, err := strconv.ParseInt(digits, 10, 64)
	return offset, err == nil && offset >= 0
}
