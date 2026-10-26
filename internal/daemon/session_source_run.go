package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var (
	sessionSeedRetryDelay          = 2 * time.Second
	sessionSeedRetryMaxDelay       = time.Minute
	sessionSnapshotRefreshInterval = 5 * time.Second
	sessionTranscriptMoveCheck     = 2 * time.Second
	sessionStreamRestartPause      = time.Second
)

func (s *sessionSource) run() {
	provider, localTranscript := s.adapter.(surface.LocalTranscriptProvider)
	if !localTranscript || !provider.RequiresLocalTranscript(&s.session) {
		if reader, ok := s.adapter.(surface.StreamCursorReader); ok {
			if cursor, err := reader.StreamCursor(s.ctx, &s.session); err == nil {
				s.streamCursor = cursor
				s.streamCursorSet = true
			} else if !errors.Is(err, surface.ErrUnsupported) && s.ctx.Err() == nil {
				s.streamCursorErr = fmt.Errorf("session stream cursor unavailable: %w", err)
			}
		}
	}
	defer func() {
		s.closeFollowers()
		s.remove()
		s.manager.running.Done()
	}()
	seedErr := s.seedJournal()
	s.setSeedErr(seedErr)
	close(s.seeded)
	if s.streamCursorErr != nil {
		s.appendSourceError(s.streamCursorErr)
		<-s.ctx.Done()
		return
	}
	for delay := sessionSeedRetryDelay; seedErr != nil; delay = min(2*delay, sessionSeedRetryMaxDelay) {
		if !s.wait(delay) {
			return
		}
		seedErr = s.seedJournal()
		s.setSeedErr(seedErr)
	}
	if !surface.EffectiveCapabilities(s.adapter, &s.session).Stream {
		for s.wait(sessionSnapshotRefreshInterval) {
			s.setSeedErr(s.seedJournal())
		}
		return
	}
	followDelay := sessionSeedRetryDelay
	var restartedAt time.Time
	for {
		current := s.session
		if refreshed, refreshErr := s.manager.registry.Session(s.session.ID); refreshErr == nil {
			current = *refreshed
		}
		if err := s.followTranscript(current); err != nil {
			if s.ctx.Err() == nil {
				s.appendSourceError(err)
			}
			if !s.wait(followDelay) {
				break
			}
			followDelay = min(2*followDelay, sessionSeedRetryMaxDelay)
			continue
		}
		followDelay = sessionSeedRetryDelay
		current.StreamCursor = s.streamCursor
		current.StreamCursorSet = s.streamCursorSet
		current.TranscriptOffset = s.session.TranscriptOffset
		current.TranscriptOffsetSet = s.session.TranscriptOffsetSet
		current.TranscriptIdentity = s.session.TranscriptIdentity
		current.CodexPendingEventUser = s.session.CodexPendingEventUser
		current.CodexPendingEventTurn = s.session.CodexPendingEventTurn
		current.CodexCurrentTurnID = s.session.CodexCurrentTurnID
		current.CodexToolWindow = s.session.CodexToolWindow
		current.ClaudeClosingTurn = s.session.ClaudeClosingTurn
		current.TranscriptFromStart = s.session.TranscriptFromStart
		streamContext, stopStream := context.WithCancel(s.ctx)
		var moved atomic.Bool
		watched := make(chan struct{})
		if _, ok := s.adapter.(surface.TranscriptContinuer); ok && current.Transcript != "" {
			go func() {
				defer close(watched)
				s.watchTranscriptMove(streamContext, current.Transcript, func() {
					moved.Store(true)
					stopStream()
				})
			}()
		} else {
			close(watched)
		}
		started := time.Now()
		var streamed atomic.Bool
		streamErr := s.adapter.Stream(streamContext, &current, "", func(event surface.StreamEvent) {
			streamed.Store(true)
			s.append(event)
		}, 30*time.Minute)
		stopStream()
		<-watched
		if moved.Load() {
			streamErr = nil
		}
		if current.Transcript == s.session.Transcript && current.TranscriptIdentity != "" && (current.TranscriptIdentity == s.session.TranscriptIdentity || !s.session.TranscriptOffsetSet) && current.TranscriptOffsetSet && current.TranscriptOffset >= s.session.TranscriptOffset {
			s.session.TranscriptOffset = current.TranscriptOffset
			s.session.TranscriptOffsetSet = true
			s.session.TranscriptIdentity = current.TranscriptIdentity
			s.session.CodexPendingEventUser = current.CodexPendingEventUser
			s.session.CodexPendingEventTurn = current.CodexPendingEventTurn
			s.session.CodexCurrentTurnID = current.CodexCurrentTurnID
			s.session.CodexToolWindow = current.CodexToolWindow
			s.session.ClaudeClosingTurn = current.ClaudeClosingTurn
			s.session.TranscriptFromStart = false
			if err := s.recordTranscriptCheckpoint(s.session.TranscriptOffset, s.session.TranscriptIdentity); err != nil {
				s.appendSourceError(fmt.Errorf("record the transcript checkpoint: %w", err))
			}
		}
		if errors.Is(streamErr, surface.ErrUnsupported) {
			<-s.ctx.Done()
			break
		}
		if streamErr != nil && !errors.Is(streamErr, surface.ErrStreamWindow) && s.ctx.Err() == nil {
			s.appendSourceError(streamErr)
		}
		s.mu.Lock()
		hasHolders := len(s.holders) > 0
		s.mu.Unlock()
		if s.ctx.Err() != nil || !hasHolders {
			break
		}
		clean := streamErr == nil || errors.Is(streamErr, surface.ErrStreamWindow)
		if clean && time.Since(started) >= sessionStreamRestartPause {
			continue
		}
		if clean && streamed.Load() && time.Since(restartedAt) >= sessionStreamRestartPause {
			restartedAt = time.Now()
			continue
		}
		select {
		case <-s.ctx.Done():
		case <-time.After(sessionStreamRestartPause):
		}
	}
}

func (s *sessionSource) followTranscript(current surface.Session) error {
	if _, ok := s.adapter.(surface.TranscriptContinuer); !ok || current.Transcript == "" || s.session.Transcript == "" || current.Transcript == s.session.Transcript {
		return nil
	}
	previous := s.session
	s.session.Transcript = current.Transcript
	s.session.TranscriptOffset = 0
	s.session.TranscriptOffsetSet = false
	ctx, cancel := context.WithTimeout(s.ctx, sessionJournalSeedTimeout)
	defer cancel()
	if err := s.catchUpLocalTranscript(ctx, previous.TranscriptIdentity, previous.Transcript); err != nil {
		s.session = previous
		return err
	}
	return nil
}

func (s *sessionSource) watchTranscriptMove(ctx context.Context, transcript string, moved func()) {
	ticker := time.NewTicker(sessionTranscriptMoveCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if refreshed, err := s.manager.registry.Session(s.session.ID); err == nil && refreshed.Transcript != "" && refreshed.Transcript != transcript {
				moved()
				return
			}
		}
	}
}

func (s *sessionSource) wait(delay time.Duration) bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}
