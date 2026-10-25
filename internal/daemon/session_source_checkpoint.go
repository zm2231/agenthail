package daemon

import (
	"context"
	"errors"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var forwardCatchUpLimit int64 = 32 << 20

func (s *sessionSource) catchUpFromCheckpoint(ctx context.Context, trustedIdentity string) (bool, error) {
	reader, ok := s.adapter.(surface.TranscriptItemReader)
	if !ok {
		return false, nil
	}
	checkpoint, found, err := s.manager.registry.SessionTranscriptCheckpoint(s.session.ID)
	if err != nil || !found || (trustedIdentity != "" && checkpoint.Identity != trustedIdentity) {
		return false, err
	}
	s.providerMu.Lock()
	read, err := reader.TranscriptItems(ctx, &s.session, surface.TranscriptItemsRequest{From: checkpoint.Offset, Identity: checkpoint.Identity, MaxBytes: forwardCatchUpLimit})
	s.providerMu.Unlock()
	if errors.Is(err, surface.ErrTranscriptChanged) || errors.Is(err, surface.ErrTranscriptBacklog) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !s.session.TranscriptOffsetSet {
		s.session.TranscriptOffset = read.End
		s.session.TranscriptOffsetSet = true
		s.session.TranscriptIdentity = read.Identity
		s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
	}
	s.appendSeedItems(read.Items)
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.Identity); err != nil {
		return false, err
	}
	return true, s.recordTranscriptCheckpoint(read.End, read.Identity)
}

func (s *sessionSource) recordTranscriptCheckpoint(offset int64, identity string) error {
	if _, ok := s.adapter.(surface.TranscriptItemReader); !ok || identity == "" {
		return nil
	}
	s.mu.Lock()
	failed := s.appendFailed
	s.mu.Unlock()
	if failed {
		return s.manager.registry.ClearSessionTranscriptCheckpoint(s.session.ID)
	}
	return s.manager.registry.SetSessionTranscriptCheckpoint(s.session.ID, registry.TranscriptCheckpoint{Offset: offset, Identity: identity})
}
