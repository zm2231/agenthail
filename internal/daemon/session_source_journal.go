package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	sessionJournalRetentionCount = 2048
	sessionJournalRetentionBytes = 8 << 20
	sessionStreamBodyBytes       = 16 << 10
)

func (s *sessionSource) append(event surface.StreamEvent) {
	if event.Kind == "commands" && s.commandsStored(event.Commands) {
		return
	}
	if event.Context != nil {
		current := s.session
		if refreshed, err := s.manager.registry.Session(s.session.ID); err == nil {
			current = *refreshed
		}
		if provider, ok := s.adapter.(surface.ContextUsageProvider); ok {
			if usage, err := provider.ContextUsage(s.ctx, &current); err == nil && usage != nil {
				event.Context = usage
			}
		}
	}
	s.mu.Lock()
	payload, bodyKey, unknownBase := s.normalizeLocked(event)
	if unknownBase != "" {
		s.mu.Unlock()
		stored, err := s.storedBody(unknownBase)
		if err != nil {
			s.mu.Lock()
			s.appendFailed = true
			s.mu.Unlock()
			s.appendSourceError(fmt.Errorf("session journal append failed: %w", err))
			return
		}
		s.mu.Lock()
		if stored != nil && s.appendBodies.get(unknownBase) == nil {
			s.appendBodies.put(unknownBase, stored)
		}
		payload, bodyKey, _ = s.normalizeLocked(event)
	}
	previous := s.appendBodies.get(bodyKey)
	s.mu.Unlock()
	var written *streamedBody
	var changed bool
	var err error
	if bodyKey != "" {
		written, changed, err = s.persistBody(payload, previous, payload.text)
	} else {
		_, changed, err = s.persist(payload)
	}
	if err != nil {
		s.mu.Lock()
		s.appendFailed = true
		s.mu.Unlock()
		s.appendSourceError(fmt.Errorf("session journal append failed: %w", err))
		return
	}
	if bodyKey != "" {
		s.mu.Lock()
		s.appendBodies.put(bodyKey, written)
		s.mu.Unlock()
	}
	if changed {
		s.wakeFollowers()
	}
	if s.manager.prompts != nil {
		s.manager.prompts(s.session, payload)
	}
}

// storedBody reads back the text the journal holds for an item, for an append
// to an item whose text the source no longer keeps.
func (s *sessionSource) storedBody(providerKey string) (*streamedBody, error) {
	entry, found, err := s.manager.registry.SessionJournalEntryByProviderKey(s.session.ID, providerKey)
	if err != nil || !found {
		return nil, err
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		return nil, err
	}
	if payload.BodyRef != "" {
		return knownBody(string(entry.FullBody), payload.BodyRef), nil
	}
	return knownBody(payload.Body, ""), nil
}

func (s *sessionSource) persist(payload sessionJournalPayload) (registry.SessionJournalEntry, bool, error) {
	entry, err := sessionJournalEntry(s.session.ID, payload)
	if err != nil {
		return registry.SessionJournalEntry{}, false, err
	}
	return s.manager.registry.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
}

func sessionJournalEntry(sessionID string, payload sessionJournalPayload) (registry.SessionJournalEntry, error) {
	return sessionJournalEntryWithRef(sessionID, payload, "")
}

func sessionJournalEntryWithRef(sessionID string, payload sessionJournalPayload, ref string) (registry.SessionJournalEntry, error) {
	fullBody := []byte(payload.Body)
	if len(fullBody) <= sessionStreamBodyBytes {
		encoded, err := json.Marshal(payload)
		return registry.SessionJournalEntry{SessionID: sessionID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, err
	}
	payload.Body = journalBodyPrefix(fullBody)
	payload.Truncated = true
	if ref != "" {
		payload.BodyRef = ref
	} else if minted, refErr := newSessionBodyRef(); refErr == nil {
		payload.BodyRef = minted
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return registry.SessionJournalEntry{}, err
	}
	if payload.BodyRef == "" || len(encoded)+len(fullBody) > sessionJournalRetentionBytes {
		payload.BodyRef = ""
		payload.TruncationReason = "full_body_not_retained"
		encoded, err = json.Marshal(payload)
		fullBody = nil
	}
	return registry.SessionJournalEntry{SessionID: sessionID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, BodyRef: payload.BodyRef, FullBody: fullBody, ObservedAt: time.Now().UTC()}, err
}

func newSessionBodyRef() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
