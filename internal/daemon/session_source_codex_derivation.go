package daemon

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexJournalDerivation = 1

func (s *sessionSource) codexTranscriptJournal() bool {
	provider, ok := s.adapter.(surface.LocalTranscriptProvider)
	return ok && s.session.Surface == surface.KindCodex && provider.RequiresLocalTranscript(&s.session)
}

func (s *sessionSource) rederiveCodexJournal(ctx context.Context) error {
	if !s.codexTranscriptJournal() {
		return nil
	}
	version, err := s.manager.registry.SessionJournalDerivation(s.session.ID)
	if err != nil || version >= codexJournalDerivation {
		return err
	}
	s.providerMu.Lock()
	read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Limit: 40})
	s.providerMu.Unlock()
	if err != nil {
		return err
	}
	if read == nil || !read.TranscriptOffsetSet {
		return fmt.Errorf("Codex local transcript is unavailable")
	}
	if err := s.restartJournal(read, read.Items); err != nil {
		return err
	}
	return s.manager.registry.MarkSessionJournalDerivation(s.session.ID, codexJournalDerivation)
}
