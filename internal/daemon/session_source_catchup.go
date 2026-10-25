package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var errTranscriptReplaced = fmt.Errorf("transcript identity changed: %w", surface.ErrTranscriptUnavailable)

func (s *sessionSource) catchUpLocalTranscript(ctx context.Context, trustedIdentity, trustedPath string) error {
	known, err := s.knownJournalItems()
	if err != nil {
		return fmt.Errorf("read journal seed identities: %w", err)
	}
	pages := make([][]surface.TimelineItem, 0, 2)
	before := int64(0)
	transcriptIdentity := trustedIdentity
	var end int64
	var dropped []surface.TimelineItem
	continued := false
	readToStart := false
	for {
		s.providerMu.Lock()
		read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Before: before, Limit: 40})
		s.providerMu.Unlock()
		if err != nil || read == nil {
			if err == nil {
				err = fmt.Errorf("session source returned no activity result")
			}
			return err
		}
		if !read.TranscriptOffsetSet {
			if len(pages) == 0 && trustedIdentity == "" && s.session.Surface == surface.KindClaude {
				held, err := s.journalHoldsTranscript()
				if err != nil {
					return err
				}
				if !held {
					s.session.TranscriptFromStart = true
					return nil
				}
			}
			return fmt.Errorf("%s transcript is unavailable", s.session.Surface)
		}
		if transcriptIdentity == "" {
			transcriptIdentity = read.TranscriptIdentity
		} else if read.TranscriptIdentity != transcriptIdentity {
			continuer, ok := s.adapter.(surface.TranscriptContinuer)
			if !ok || len(pages) > 0 {
				phase := "during catch-up"
				if len(pages) == 0 {
					phase = "before catch-up"
				}
				return fmt.Errorf("%s transcript was replaced %s: %w", s.session.Surface, phase, errTranscriptReplaced)
			}
			s.providerMu.Lock()
			dropped, continued, err = continuer.TranscriptContinuation(ctx, &s.session, trustedPath, transcriptIdentity)
			s.providerMu.Unlock()
			if err != nil {
				return err
			}
			if !continued {
				return s.restartJournal(read)
			}
			transcriptIdentity = read.TranscriptIdentity
		}
		if read.TranscriptOffsetSet && !s.session.TranscriptOffsetSet {
			s.session.TranscriptOffset = read.TranscriptOffset
			s.session.TranscriptOffsetSet = true
			s.session.CodexPendingEventUser = read.CodexPendingEventUser
			s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
			s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
			s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
			s.session.TranscriptIdentity = read.TranscriptIdentity
		}
		if len(pages) == 0 {
			end = read.TranscriptOffset
		}
		pages = append(pages, read.Items)
		if timelineItemsOverlapJournal(read.Items, known) {
			break
		}
		if read.NextBefore == 0 {
			readToStart = true
			break
		}
		before = read.NextBefore
	}
	for index := len(pages) - 1; index >= 0; index-- {
		s.appendSeedItems(pages[index])
	}
	if continued {
		s.removeDroppedItems(dropped, known)
	}
	if continued && readToStart {
		if err := s.manager.registry.LowerSessionJournalHistoryBoundary(s.session.ID, 0); err != nil {
			return err
		}
	}
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, transcriptIdentity); err != nil {
		return err
	}
	return s.recordTranscriptCheckpoint(end, transcriptIdentity)
}

func (s *sessionSource) removeDroppedItems(items []surface.TimelineItem, known map[string]struct{}) {
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		event := seedEvent(s.session.Surface, item)
		if _, ok := known[event.ProviderKey]; !ok {
			continue
		}
		s.append(surface.StreamEvent{ID: event.ID, ProviderKey: event.ProviderKey, Operation: "remove", Kind: item.Kind, TurnID: item.TurnID, Final: true})
	}
}

func (s *sessionSource) journalHoldsTranscript() (bool, error) {
	before := uint64(0)
	for {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		if err != nil {
			var gap *registry.SessionJournalHistoryGapError
			if errors.As(err, &gap) {
				return true, nil
			}
			return false, err
		}
		for _, entry := range page.Entries {
			if entry.Kind != "source-error" {
				return true, nil
			}
		}
		if page.NextBefore == 0 {
			return false, nil
		}
		before = page.NextBefore
	}
}

func (s *sessionSource) knownJournalItems() (map[string]struct{}, error) {
	known := map[string]struct{}{}
	before := uint64(0)
	for {
		page, err := s.manager.registry.ReadSessionJournalPage(s.session.ID, before, 200)
		if err != nil {
			var gap *registry.SessionJournalHistoryGapError
			if errors.As(err, &gap) {
				return known, nil
			}
			return nil, err
		}
		for _, entry := range page.Entries {
			var payload sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				continue
			}
			if payload.ItemID != "" {
				known[payload.ItemID] = struct{}{}
			}
			if payload.ProviderKey != "" {
				known[payload.ProviderKey] = struct{}{}
			}
		}
		if page.NextBefore == 0 {
			return known, nil
		}
		before = page.NextBefore
	}
}

func timelineItemsOverlapJournal(items []surface.TimelineItem, known map[string]struct{}) bool {
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, ok := known[item.ID]; ok {
			return true
		}
		if _, ok := known["timeline:"+item.ID]; ok {
			return true
		}
	}
	return false
}
