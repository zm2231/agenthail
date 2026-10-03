package registry

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SessionJournalEntry struct {
	SessionID   string
	Seq         uint64
	Kind        string
	ProviderKey string
	Payload     []byte
	ObservedAt  time.Time
	Bytes       int
}

type SessionJournalRetention struct {
	Count int
	Bytes int
}

type SessionJournalWindow struct {
	Entries     []SessionJournalEntry
	EarliestSeq uint64
	LatestSeq   uint64
	Gap         bool
}

func (r *Registry) AppendSessionJournalEntry(input SessionJournalEntry, retention SessionJournalRetention) (SessionJournalEntry, bool, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.Kind) == "" {
		return SessionJournalEntry{}, false, fmt.Errorf("session journal entry requires session and kind")
	}
	if retention.Count < 1 || retention.Bytes < 1 {
		return SessionJournalEntry{}, false, fmt.Errorf("session journal retention requires positive count and bytes")
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now().UTC()
	} else {
		input.ObservedAt = input.ObservedAt.UTC()
	}
	input.Payload = append([]byte(nil), input.Payload...)
	input.Bytes = len(input.Payload)
	if input.Bytes > retention.Bytes {
		return SessionJournalEntry{}, false, fmt.Errorf("session journal entry exceeds byte retention")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return SessionJournalEntry{}, false, err
	}
	defer tx.Rollback()
	if input.ProviderKey != "" {
		existing, found, err := sessionJournalByProviderKey(tx, input.SessionID, input.ProviderKey)
		if err != nil {
			return SessionJournalEntry{}, false, err
		}
		if found {
			if _, err := tx.Exec(`UPDATE session_journal SET kind=?,payload=?,observed_at=?,bytes=? WHERE session_id=? AND seq=?`, input.Kind, input.Payload, input.ObservedAt.Format(time.RFC3339Nano), input.Bytes, input.SessionID, existing.Seq); err != nil {
				return SessionJournalEntry{}, false, err
			}
			existing.Kind = input.Kind
			existing.Payload = input.Payload
			existing.ObservedAt = input.ObservedAt
			existing.Bytes = input.Bytes
			if err := trimSessionJournal(tx, input.SessionID, retention); err != nil {
				return SessionJournalEntry{}, false, err
			}
			return existing, false, tx.Commit()
		}
	}
	if _, err := tx.Exec(`INSERT INTO session_journal_state(session_id,next_seq,retained_bytes) VALUES(?,0,0) ON CONFLICT(session_id) DO NOTHING`, input.SessionID); err != nil {
		return SessionJournalEntry{}, false, err
	}
	var next int64
	if err := tx.QueryRow(`SELECT next_seq FROM session_journal_state WHERE session_id=?`, input.SessionID).Scan(&next); err != nil {
		return SessionJournalEntry{}, false, err
	}
	if next < 0 {
		return SessionJournalEntry{}, false, fmt.Errorf("invalid session journal sequence")
	}
	input.Seq = uint64(next + 1)
	if _, err := tx.Exec(`INSERT INTO session_journal(session_id,seq,kind,provider_key,payload,observed_at,bytes) VALUES(?,?,?,?,?,?,?)`, input.SessionID, input.Seq, input.Kind, input.ProviderKey, input.Payload, input.ObservedAt.Format(time.RFC3339Nano), input.Bytes); err != nil {
		return SessionJournalEntry{}, false, err
	}
	if _, err := tx.Exec(`UPDATE session_journal_state SET next_seq=? WHERE session_id=?`, input.Seq, input.SessionID); err != nil {
		return SessionJournalEntry{}, false, err
	}
	if err := trimSessionJournal(tx, input.SessionID, retention); err != nil {
		return SessionJournalEntry{}, false, err
	}
	return input, true, tx.Commit()
}

func (r *Registry) SessionJournalAfter(sessionID string, after uint64, limit int) (SessionJournalWindow, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionJournalWindow{}, fmt.Errorf("session id is required")
	}
	if limit < 1 {
		return SessionJournalWindow{Entries: []SessionJournalEntry{}}, nil
	}
	var earliest, latest sql.NullInt64
	if err := r.db.QueryRow(`SELECT MIN(seq),MAX(seq) FROM session_journal WHERE session_id=?`, sessionID).Scan(&earliest, &latest); err != nil {
		return SessionJournalWindow{}, err
	}
	window := SessionJournalWindow{Entries: []SessionJournalEntry{}}
	if !earliest.Valid || !latest.Valid {
		return window, nil
	}
	window.EarliestSeq = uint64(earliest.Int64)
	window.LatestSeq = uint64(latest.Int64)
	window.Gap = after > 0 && (after < window.EarliestSeq-1 || after > window.LatestSeq)
	if window.Gap {
		return window, nil
	}
	rows, err := r.db.Query(`SELECT session_id,seq,kind,provider_key,payload,observed_at,bytes FROM session_journal WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?`, sessionID, after, limit)
	if err != nil {
		return SessionJournalWindow{}, err
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanSessionJournalEntry(rows)
		if err != nil {
			return SessionJournalWindow{}, err
		}
		window.Entries = append(window.Entries, entry)
	}
	return window, rows.Err()
}

type sessionJournalRow interface {
	Scan(...any) error
}

func sessionJournalByProviderKey(q interface{ QueryRow(string, ...any) *sql.Row }, sessionID, providerKey string) (SessionJournalEntry, bool, error) {
	entry, err := scanSessionJournalEntry(q.QueryRow(`SELECT session_id,seq,kind,provider_key,payload,observed_at,bytes FROM session_journal WHERE session_id=? AND provider_key=?`, sessionID, providerKey))
	if err == sql.ErrNoRows {
		return SessionJournalEntry{}, false, nil
	}
	return entry, err == nil, err
}

func scanSessionJournalEntry(row sessionJournalRow) (SessionJournalEntry, error) {
	var entry SessionJournalEntry
	var seq int64
	var observedAt string
	if err := row.Scan(&entry.SessionID, &seq, &entry.Kind, &entry.ProviderKey, &entry.Payload, &observedAt, &entry.Bytes); err != nil {
		return SessionJournalEntry{}, err
	}
	if seq < 1 {
		return SessionJournalEntry{}, fmt.Errorf("invalid session journal sequence %d", seq)
	}
	parsed, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return SessionJournalEntry{}, fmt.Errorf("parse session journal observation time: %w", err)
	}
	entry.Seq = uint64(seq)
	entry.ObservedAt = parsed
	entry.Payload = append([]byte(nil), entry.Payload...)
	return entry, nil
}

func trimSessionJournal(tx *sql.Tx, sessionID string, retention SessionJournalRetention) error {
	for {
		var count, bytes int
		if err := tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(bytes),0) FROM session_journal WHERE session_id=?`, sessionID).Scan(&count, &bytes); err != nil {
			return err
		}
		if count <= retention.Count && bytes <= retention.Bytes {
			return refreshSessionJournalState(tx, sessionID)
		}
		if _, err := tx.Exec(`DELETE FROM session_journal WHERE session_id=? AND seq=(SELECT seq FROM session_journal WHERE session_id=? ORDER BY seq LIMIT 1)`, sessionID, sessionID); err != nil {
			return err
		}
	}
}

func refreshSessionJournalState(tx *sql.Tx, sessionID string) error {
	_, err := tx.Exec(`UPDATE session_journal_state SET retained_bytes=COALESCE((SELECT SUM(bytes) FROM session_journal WHERE session_id=?),0) WHERE session_id=?`, sessionID, sessionID)
	return err
}
