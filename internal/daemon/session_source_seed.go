package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const sessionJournalSeedTimeout = 12 * time.Second

var (
	sessionPageHandoffGrace = 5 * time.Second
	sessionPageCatchUpWait  = 2 * time.Second
)

func (m *sessionSourceManager) seed(ctx context.Context, session *surface.Session, adapter surface.Surface) error {
	source, release, err := m.holdSource(session, adapter, "page-seed")
	if err != nil {
		return err
	}
	defer func() { time.AfterFunc(sessionPageHandoffGrace, release) }()
	select {
	case <-source.seeded:
		return source.seedError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *sessionSourceManager) readHistory(ctx context.Context, session *surface.Session, adapter surface.Surface, before int64, limit int) (*surface.SessionReadResult, error) {
	source, release, err := m.holdSource(session, adapter, "history-read")
	if err != nil {
		return nil, err
	}
	defer func() { time.AfterFunc(sessionPageHandoffGrace, release) }()
	select {
	case <-source.seeded:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	source.providerMu.Lock()
	defer source.providerMu.Unlock()
	request := *session
	read, err := surface.ReadSession(ctx, adapter, &request, surface.SessionReadRequest{Before: before, Limit: limit})
	if err != nil {
		return nil, err
	}
	if read == nil {
		return nil, fmt.Errorf("session source returned no older activity")
	}
	return read, nil
}

func (s *sessionSource) appendSourceError(streamErr error) {
	_ = s.manager.registry.MarkSessionJournalSeed(s.session.ID, false)
	s.mu.Lock()
	s.sourceVersion++
	payload := sessionJournalPayload{
		ItemID:      s.epoch + ":source",
		ProviderKey: s.epoch + ":source",
		Version:     s.sourceVersion,
		Op:          "reset",
		Kind:        "source-error",
		TS:          time.Now().UTC().Format(time.RFC3339Nano),
		Reason:      boundedSessionSourceReason(streamErr.Error()),
	}
	s.mu.Unlock()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, changed, err := s.manager.registry.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		return
	}
	if changed {
		s.wakeFollowers()
	}
}

func boundedSessionSourceReason(value string) string {
	const limit = 240
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "The session source stopped unexpectedly."
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func (s *sessionSource) seedJournal() error {
	err := s.loadSeed()
	if err != nil && s.ctx.Err() == nil {
		s.appendSourceError(err)
	}
	return err
}

func (s *sessionSource) loadSeed() error {
	seedStatus, seedSeq, trustedIdentity, statusErr := s.manager.registry.SessionJournalSeedCheckpoint(s.session.ID)
	localTranscript := false
	if provider, ok := s.adapter.(surface.LocalTranscriptProvider); ok {
		localTranscript = provider.RequiresLocalTranscript(&s.session)
	}
	transcriptBacked := localTranscript || s.session.Surface == surface.KindClaude
	streamable := surface.EffectiveCapabilities(s.adapter, &s.session).Stream
	seeded := statusErr == nil && seedStatus == registry.SessionJournalSeeded
	if seeded && !transcriptBacked && streamable {
		return nil
	}
	if localTranscript && trustedIdentity == "" && seedSeq > 0 {
		return fmt.Errorf("Codex local transcript identity checkpoint is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	ctx, cancel := context.WithTimeout(s.ctx, sessionJournalSeedTimeout)
	defer cancel()
	if seeded && transcriptBacked {
		caughtUp, err := s.catchUpFromCheckpoint(ctx, trustedIdentity)
		if err == nil && !caughtUp {
			err = s.catchUpLocalTranscript(ctx, trustedIdentity, "")
		}
		if err == nil {
			err = s.rederiveJournal(ctx)
		}
		if localTranscript || !errors.Is(err, errTranscriptReplaced) {
			return err
		}
		trustedIdentity = ""
	}
	s.providerMu.Lock()
	read, err := surface.ReadSession(ctx, s.adapter, &s.session, surface.SessionReadRequest{Limit: 40})
	s.providerMu.Unlock()
	if err != nil || read == nil {
		if err == nil {
			err = fmt.Errorf("session source returned no activity result")
		}
		return err
	}
	if trustedIdentity != "" && read.TranscriptIdentity != trustedIdentity {
		if _, continues := s.adapter.(surface.TranscriptContinuer); continues {
			return s.catchUpLocalTranscript(ctx, trustedIdentity, "")
		}
		return fmt.Errorf("Codex local transcript was replaced before catch-up: %w", errTranscriptReplaced)
	}
	if read.TranscriptOffsetSet {
		s.session.TranscriptOffset = read.TranscriptOffset
		s.session.TranscriptOffsetSet = true
		s.session.CodexPendingEventUser = read.CodexPendingEventUser
		s.session.CodexPendingEventTurn = read.CodexPendingEventTurn
		s.session.CodexCurrentTurnID = read.CodexCurrentTurnID
		s.session.CodexToolWindow = read.CodexToolWindow
		s.session.ClaudeClosingTurn = read.ClaudeClosingTurn
		s.session.TranscriptIdentity = read.TranscriptIdentity
	} else if localTranscript {
		return fmt.Errorf("Codex local transcript is unavailable")
	}
	s.session.TranscriptFromStart = !read.TranscriptOffsetSet
	if localTranscript && read.TranscriptIdentity == "" {
		return fmt.Errorf("Codex local transcript identity is unavailable: %w", surface.ErrTranscriptUnavailable)
	}
	if err := s.manager.registry.RecordSessionJournalHistoryBoundary(s.session.ID, max(0, read.NextBefore)); err != nil {
		return err
	}
	s.appendSeedItems(read.Items)
	if err := s.manager.registry.MarkSessionJournalSeedWithIdentity(s.session.ID, true, read.TranscriptIdentity); err != nil {
		return err
	}
	if s.codexTranscriptJournal() {
		return s.manager.registry.MarkSessionJournalDerivation(s.session.ID, codexJournalDerivation)
	}
	if _, ok := s.adapter.(surface.TranscriptItemReader); ok {
		if err := s.recordTranscriptCheckpoint(read.TranscriptOffset, read.TranscriptIdentity); err != nil {
			return err
		}
		return s.manager.registry.MarkSessionJournalDerivation(s.session.ID, transcriptJournalDerivation)
	}
	return nil
}

func (s *sessionSource) setSeedErr(err error) {
	s.mu.Lock()
	s.seedErr = err
	s.mu.Unlock()
}

func (s *sessionSource) seedError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedErr
}
