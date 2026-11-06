package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
)

type streamedBody struct {
	text   []byte
	ref    string
	stored int
}

func knownBody(body, ref string) *streamedBody {
	known := &streamedBody{text: []byte(body), ref: ref}
	if ref != "" {
		known.stored = len(known.text)
	}
	return known
}

func (s *sessionSource) persistBody(payload sessionJournalPayload, previous *streamedBody, text []byte) (*streamedBody, bool, error) {
	if previous != nil && previous.ref != "" && previous.stored > 0 && len(text) > max(previous.stored, sessionStreamBodyBytes) && bytes.Equal(text[:previous.stored], previous.text[:previous.stored]) {
		payload.Body, payload.Truncated, payload.BodyRef = journalBodyPrefix(text), true, previous.ref
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, false, err
		}
		entry := registry.SessionJournalEntry{SessionID: s.session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, BodyRef: previous.ref, ObservedAt: time.Now().UTC()}
		_, err = s.manager.registry.AppendSessionJournalBody(entry, previous.stored, text[previous.stored:], registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
		if err == nil {
			return &streamedBody{text: text, ref: previous.ref, stored: len(text)}, true, nil
		}
		if !errors.Is(err, registry.ErrSessionJournalBodyMoved) && !errors.Is(err, registry.ErrSessionJournalEntryTooLarge) {
			return nil, false, err
		}
	}
	ref := ""
	if previous != nil {
		ref = previous.ref
	}
	payload.Body = string(text)
	entry, err := sessionJournalEntryWithRef(s.session.ID, payload, ref)
	if err != nil {
		return nil, false, err
	}
	_, changed, err := s.manager.registry.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil {
		return nil, false, err
	}
	written := &streamedBody{text: text, ref: entry.BodyRef}
	if entry.BodyRef != "" {
		written.stored = len(text)
	}
	return written, changed, nil
}

func journalBodyPrefix(text []byte) string {
	prefix := text[:min(len(text), sessionStreamBodyBytes)]
	for !utf8.Valid(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return string(prefix)
}
