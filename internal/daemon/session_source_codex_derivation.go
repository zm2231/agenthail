package daemon

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

// 2: managed sessions read their timeline from the rollout, so rows their
// app-server stream journaled are read again from it.
const codexJournalDerivation = 2

func (s *sessionSource) codexTranscriptJournal() bool {
	provider, ok := s.adapter.(surface.LocalTranscriptProvider)
	return ok && s.session.Surface == surface.KindCodex && provider.RequiresLocalTranscript(&s.session)
}

// rebuildStaleCodexJournal reads a journal an older derivation wrote again
// from the newest page of its rollout, and reports whether it did. It runs
// before any catch-up, which would otherwise walk the rollout back to rows
// the rebuild replaces.
func (s *sessionSource) rebuildStaleCodexJournal(ctx context.Context) (bool, error) {
	if !s.codexTranscriptJournal() {
		return false, nil
	}
	version, err := s.manager.registry.SessionJournalDerivation(s.session.ID)
	if err != nil || version >= codexJournalDerivation {
		return false, err
	}
	s.providerMu.Lock()
	read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Limit: 40})
	s.providerMu.Unlock()
	if err != nil {
		return false, err
	}
	if read == nil {
		return false, fmt.Errorf("Codex local transcript is unavailable")
	}
	if !read.TranscriptOffsetSet {
		held, err := s.journalHoldsTranscript()
		if err != nil {
			return false, err
		}
		if held {
			return false, fmt.Errorf("Codex local transcript is unavailable")
		}
		return false, s.manager.registry.MarkSessionJournalDerivation(s.session.ID, codexJournalDerivation)
	}
	if err := s.restartJournal(read, read.Items); err != nil {
		return false, err
	}
	return true, s.manager.registry.MarkSessionJournalDerivation(s.session.ID, codexJournalDerivation)
}
