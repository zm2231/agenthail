package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

var errSessionJournalGap = errors.New("the session journal no longer holds the rows after this cursor; reselect the session")

const sessionJournalFollowPage = 64

func readSessionJournalAfter(store *registry.Registry, sessionID string, cursor *uint64, yield func(registry.SessionJournalEntry) bool) (bool, error) {
	for {
		window, err := store.SessionJournalAfter(sessionID, *cursor, sessionJournalFollowPage)
		if err != nil {
			return false, err
		}
		if window.Gap {
			return false, errSessionJournalGap
		}
		for _, entry := range window.Entries {
			if !yield(entry) {
				return false, nil
			}
			*cursor = entry.Seq
		}
		if len(window.Entries) < sessionJournalFollowPage {
			return true, nil
		}
	}
}

func (m *sessionSourceManager) prepareStream(ctx context.Context, session *surface.Session, adapter surface.Surface) (sessionstream.Subscription, error) {
	subscription, err := m.subscribeContext(ctx, session, adapter)
	if err != nil {
		return sessionstream.Subscription{}, err
	}
	m.mu.Lock()
	source := m.sources[session.ID]
	m.mu.Unlock()
	if source == nil {
		subscription.Cancel()
		return sessionstream.Subscription{}, fmt.Errorf("session source disappeared while preparing stream")
	}
	select {
	case <-source.seeded:
	case <-ctx.Done():
		subscription.Cancel()
		return sessionstream.Subscription{}, ctx.Err()
	}
	if err := source.seedError(); err != nil {
		subscription.Cancel()
		return sessionstream.Subscription{}, fmt.Errorf("seed session source: %w", err)
	}
	window, err := m.registry.SessionJournalAfter(session.ID, 0, 1)
	if err != nil {
		subscription.Cancel()
		return sessionstream.Subscription{}, fmt.Errorf("capture session journal cursor: %w", err)
	}
	events := make(chan sessionstream.Event, 64)
	done := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() { close(done) })
		subscription.Cancel()
	}
	go func() {
		defer close(events)
		cursor := window.LatestSeq
		send := func(event sessionstream.Event) bool {
			select {
			case events <- event:
				return true
			case <-done:
				return false
			}
		}
		follow := func() bool {
			more, err := readSessionJournalAfter(m.registry, session.ID, &cursor, func(entry registry.SessionJournalEntry) bool {
				event, decodeErr := sessionstream.DecodePayload(entry.Seq, entry.Payload)
				if decodeErr != nil {
					send(sessionstream.Event{Seq: entry.Seq, Kind: "source-error", Reason: decodeErr.Error()})
					return false
				}
				return send(event)
			})
			if err != nil {
				send(sessionstream.Event{Seq: cursor, Kind: "source-error", Reason: err.Error()})
			}
			return more
		}
		for {
			select {
			case <-done:
				return
			case _, open := <-subscription.Wake:
				if !follow() || !open {
					return
				}
			}
		}
	}()
	return sessionstream.Subscription{Cursor: window.LatestSeq, Events: events, Cancel: cancel}, nil
}
