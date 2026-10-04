package registry

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrSessionJournalEntryTooLarge = errors.New("session journal entry exceeds byte retention")

type SessionJournalHistoryGapError struct {
	EarliestSeq uint64
	LatestSeq   uint64
}

func (e *SessionJournalHistoryGapError) Error() string {
	return "session journal history cursor is no longer retained"
}

type SessionJournalEntry struct {
	SessionID   string
	Seq         uint64
	Kind        string
	ProviderKey string
	Payload     []byte
	ObservedAt  time.Time
	Bytes       int
	BodyRef     string
	FullBody    []byte
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

type SessionJournalPage struct {
	Entries    []SessionJournalEntry
	NextBefore uint64
	LatestSeq  uint64
}

const (
	SessionJournalSeedUnknown = "unknown"
	SessionJournalSeedFailed  = "failed"
	SessionJournalSeeded      = "succeeded"
)

func (r *Registry) SessionJournalSeedStatus(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session id is required")
	}
	var status string
	err := r.db.QueryRow(`SELECT seed_status FROM session_journal_state WHERE session_id=?`, sessionID).Scan(&status)
	if err == sql.ErrNoRows {
		return SessionJournalSeedUnknown, nil
	}
	return status, err
}

func (r *Registry) MarkSessionJournalSeed(sessionID string, succeeded bool) error {
	return r.MarkSessionJournalSeedWithIdentity(sessionID, succeeded, "")
}

func (r *Registry) MarkSessionJournalSeedWithIdentity(sessionID string, succeeded bool, transcriptIdentity string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	status := SessionJournalSeedFailed
	if succeeded {
		status = SessionJournalSeeded
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seedSeq := uint64(0)
	if succeeded {
		var latest sql.NullInt64
		if err := tx.QueryRow(`SELECT MAX(seq) FROM session_journal WHERE session_id=?`, sessionID).Scan(&latest); err != nil {
			return err
		}
		if latest.Valid && latest.Int64 > 0 {
			seedSeq = uint64(latest.Int64)
		}
		_, err = tx.Exec(`INSERT INTO session_journal_state(session_id,next_seq,retained_bytes,seed_status,seed_seq,seed_identity) VALUES(?,0,0,?,?,?) ON CONFLICT(session_id) DO UPDATE SET seed_status=excluded.seed_status,seed_seq=excluded.seed_seq,seed_identity=CASE WHEN excluded.seed_identity!='' THEN excluded.seed_identity ELSE session_journal_state.seed_identity END`, sessionID, status, seedSeq, transcriptIdentity)
	} else {
		_, err = tx.Exec(`INSERT INTO session_journal_state(session_id,next_seq,retained_bytes,seed_status) VALUES(?,0,0,?) ON CONFLICT(session_id) DO UPDATE SET seed_status=excluded.seed_status`, sessionID, status)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) SessionJournalSeedCheckpoint(sessionID string) (string, uint64, string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", 0, "", fmt.Errorf("session id is required")
	}
	var status string
	var seq int64
	var identity string
	err := r.db.QueryRow(`SELECT seed_status,seed_seq,seed_identity FROM session_journal_state WHERE session_id=?`, sessionID).Scan(&status, &seq, &identity)
	if err == sql.ErrNoRows {
		return SessionJournalSeedUnknown, 0, "", nil
	}
	if err != nil {
		return "", 0, "", err
	}
	if seq < 0 {
		return "", 0, "", fmt.Errorf("invalid session journal seed sequence")
	}
	return status, uint64(seq), identity, nil
}

func (r *Registry) ReadSessionJournalPage(sessionID string, before uint64, limit int) (SessionJournalPage, error) {
	if strings.TrimSpace(sessionID) == "" || limit < 1 || limit > 200 {
		return SessionJournalPage{}, fmt.Errorf("invalid journal page request")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return SessionJournalPage{}, err
	}
	defer tx.Rollback()
	var earliest, latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MIN(seq),MAX(seq) FROM session_journal WHERE session_id=?`, sessionID).Scan(&earliest, &latest); err != nil {
		return SessionJournalPage{}, err
	}
	var prunedBefore sql.NullInt64
	if err := tx.QueryRow(`SELECT pruned_before FROM session_journal_state WHERE session_id=?`, sessionID).Scan(&prunedBefore); err != nil && err != sql.ErrNoRows {
		return SessionJournalPage{}, err
	}
	if before > 0 {
		if !earliest.Valid || (prunedBefore.Valid && uint64(prunedBefore.Int64) >= before-1) {
			var earliestSeq, latestSeq uint64
			if earliest.Valid {
				earliestSeq = uint64(earliest.Int64)
			}
			if latest.Valid {
				latestSeq = uint64(latest.Int64)
			}
			return SessionJournalPage{}, &SessionJournalHistoryGapError{EarliestSeq: earliestSeq, LatestSeq: latestSeq}
		}
		earliestSeq, latestSeq := uint64(earliest.Int64), uint64(latest.Int64)
		if before < earliestSeq-1 {
			return SessionJournalPage{}, &SessionJournalHistoryGapError{EarliestSeq: earliestSeq, LatestSeq: latestSeq}
		}
	}
	rows, err := tx.Query(`SELECT session_id,seq,kind,provider_key,payload,observed_at,bytes,body_ref FROM session_journal WHERE session_id=? AND (?=0 OR seq<?) ORDER BY seq DESC LIMIT ?`, sessionID, before, before, limit+1)
	if err != nil {
		return SessionJournalPage{}, err
	}
	defer rows.Close()
	entries := make([]SessionJournalEntry, 0, limit+1)
	for rows.Next() {
		entry, err := scanSessionJournalEntry(rows)
		if err != nil {
			return SessionJournalPage{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return SessionJournalPage{}, err
	}
	var next uint64
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[len(entries)-1].Seq
	} else if prunedBefore.Valid && prunedBefore.Int64 > 0 && len(entries) > 0 && entries[len(entries)-1].Seq == uint64(earliest.Int64) {
		next = entries[len(entries)-1].Seq
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	if err := rows.Close(); err != nil {
		return SessionJournalPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionJournalPage{}, err
	}
	return SessionJournalPage{Entries: entries, NextBefore: next, LatestSeq: uint64(latest.Int64)}, nil
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
	input.FullBody = append([]byte(nil), input.FullBody...)
	input.Bytes = len(input.Payload) + len(input.FullBody)
	if input.Bytes > retention.Bytes {
		return SessionJournalEntry{}, false, fmt.Errorf("%w: %d > %d", ErrSessionJournalEntryTooLarge, input.Bytes, retention.Bytes)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return SessionJournalEntry{}, false, err
	}
	defer tx.Rollback()
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
	if input.ProviderKey != "" {
		existing, found, err := sessionJournalByProviderKey(tx, input.SessionID, input.ProviderKey)
		if err != nil {
			return SessionJournalEntry{}, false, err
		}
		if found {
			existingBody, existingBodyFound, err := sessionJournalBody(tx, existing)
			if err != nil {
				return SessionJournalEntry{}, false, err
			}
			if sameSessionJournalContent(existing, input, existingBody, existingBodyFound) {
				return existing, false, tx.Commit()
			}
			if existing.BodyRef != "" && existing.BodyRef != input.BodyRef {
				if _, err := tx.Exec(`DELETE FROM session_journal_bodies WHERE session_id=? AND ref=?`, input.SessionID, existing.BodyRef); err != nil {
					return SessionJournalEntry{}, false, err
				}
			}
			if _, err := tx.Exec(`UPDATE session_journal SET seq=?,kind=?,payload=?,observed_at=?,bytes=?,body_ref=? WHERE session_id=? AND seq=?`, input.Seq, input.Kind, input.Payload, input.ObservedAt.Format(time.RFC3339Nano), input.Bytes, input.BodyRef, input.SessionID, existing.Seq); err != nil {
				return SessionJournalEntry{}, false, err
			}
			if err := storeSessionJournalBody(tx, input); err != nil {
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
	}
	if _, err := tx.Exec(`INSERT INTO session_journal(session_id,seq,kind,provider_key,payload,observed_at,bytes,body_ref) VALUES(?,?,?,?,?,?,?,?)`, input.SessionID, input.Seq, input.Kind, input.ProviderKey, input.Payload, input.ObservedAt.Format(time.RFC3339Nano), input.Bytes, input.BodyRef); err != nil {
		return SessionJournalEntry{}, false, err
	}
	if err := storeSessionJournalBody(tx, input); err != nil {
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

func sameSessionJournalContent(existing, input SessionJournalEntry, existingBody []byte, existingBodyFound bool) bool {
	if existing.Kind != input.Kind || !sameSessionJournalPayload(existing.Payload, input.Payload) {
		return false
	}
	if (existing.BodyRef == "") != (input.BodyRef == "") {
		return false
	}
	if existing.BodyRef != "" && !existingBodyFound {
		return false
	}
	return bytes.Equal(existingBody, input.FullBody)
}

func sameSessionJournalPayload(existing, input []byte) bool {
	if bytes.Equal(existing, input) {
		return true
	}
	var existingObject, inputObject map[string]any
	if json.Unmarshal(existing, &existingObject) != nil || json.Unmarshal(input, &inputObject) != nil {
		return false
	}
	delete(existingObject, "bodyRef")
	delete(inputObject, "bodyRef")
	return reflect.DeepEqual(existingObject, inputObject)
}

func sessionJournalBody(q interface{ QueryRow(string, ...any) *sql.Row }, entry SessionJournalEntry) ([]byte, bool, error) {
	if entry.BodyRef == "" {
		return nil, true, nil
	}
	var body []byte
	if err := q.QueryRow(`SELECT body FROM session_journal_bodies WHERE session_id=? AND ref=?`, entry.SessionID, entry.BodyRef).Scan(&body); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return body, true, nil
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
	rows, err := r.db.Query(`SELECT session_id,seq,kind,provider_key,payload,observed_at,bytes,body_ref FROM session_journal WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?`, sessionID, after, limit)
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

func (r *Registry) SessionJournalEntryByProviderKey(sessionID, providerKey string) (SessionJournalEntry, bool, error) {
	entry, found, err := sessionJournalByProviderKey(r.db, sessionID, providerKey)
	if err != nil || !found || entry.BodyRef == "" {
		return entry, found, err
	}
	if err := r.db.QueryRow(`SELECT body FROM session_journal_bodies WHERE session_id=? AND ref=?`, sessionID, entry.BodyRef).Scan(&entry.FullBody); err != nil && err != sql.ErrNoRows {
		return SessionJournalEntry{}, false, err
	}
	return entry, true, nil
}

func sessionJournalByProviderKey(q interface{ QueryRow(string, ...any) *sql.Row }, sessionID, providerKey string) (SessionJournalEntry, bool, error) {
	entry, err := scanSessionJournalEntry(q.QueryRow(`SELECT session_id,seq,kind,provider_key,payload,observed_at,bytes,body_ref FROM session_journal WHERE session_id=? AND provider_key=?`, sessionID, providerKey))
	if err == sql.ErrNoRows {
		return SessionJournalEntry{}, false, nil
	}
	return entry, err == nil, err
}

func scanSessionJournalEntry(row sessionJournalRow) (SessionJournalEntry, error) {
	var entry SessionJournalEntry
	var seq int64
	var observedAt string
	if err := row.Scan(&entry.SessionID, &seq, &entry.Kind, &entry.ProviderKey, &entry.Payload, &observedAt, &entry.Bytes, &entry.BodyRef); err != nil {
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
		var prunedSeq int64
		if err := tx.QueryRow(`SELECT seq FROM session_journal WHERE session_id=? ORDER BY seq LIMIT 1`, sessionID).Scan(&prunedSeq); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM session_journal WHERE session_id=? AND seq=?`, sessionID, prunedSeq); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE session_journal_state SET pruned_before=MAX(pruned_before,?) WHERE session_id=?`, prunedSeq, sessionID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM session_journal_bodies WHERE session_id=? AND ref NOT IN (SELECT body_ref FROM session_journal WHERE session_id=? AND body_ref!='')`, sessionID, sessionID); err != nil {
			return err
		}
	}
}

func storeSessionJournalBody(tx *sql.Tx, entry SessionJournalEntry) error {
	if entry.BodyRef == "" {
		return nil
	}
	if len(entry.FullBody) == 0 {
		return fmt.Errorf("session journal body reference requires a body")
	}
	_, err := tx.Exec(`INSERT INTO session_journal_bodies(ref,session_id,body,created_at) VALUES(?,?,?,?) ON CONFLICT(ref) DO UPDATE SET body=excluded.body,session_id=excluded.session_id,created_at=excluded.created_at`, entry.BodyRef, entry.SessionID, entry.FullBody, entry.ObservedAt.Format(time.RFC3339Nano))
	return err
}

func (r *Registry) SessionJournalBody(sessionID, ref string, start, end int) ([]byte, int, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(ref) == "" || start < 0 || end < start {
		return nil, 0, fmt.Errorf("invalid session journal body range")
	}
	if start == int(^uint(0)>>1) || end-start == int(^uint(0)>>1) {
		return nil, 0, fmt.Errorf("invalid session journal body range")
	}
	var body []byte
	var total int
	if err := r.db.QueryRow(`SELECT length(body),substr(body,?,?) FROM session_journal_bodies WHERE session_id=? AND ref=?`, start+1, end-start+1, sessionID, ref).Scan(&total, &body); err != nil {
		return nil, 0, err
	}
	if start > total {
		return nil, 0, fmt.Errorf("session journal body range is outside the retained body")
	}
	if start < total && len(body) > 0 && !utf8.RuneStart(body[0]) {
		return nil, 0, fmt.Errorf("session journal body range starts inside a UTF-8 code point")
	}
	if end > total {
		end = total
	}
	for end > start && end < total && !utf8.RuneStart(body[end-start]) {
		end--
	}
	return append([]byte(nil), body[:end-start]...), total, nil
}

func refreshSessionJournalState(tx *sql.Tx, sessionID string) error {
	_, err := tx.Exec(`UPDATE session_journal_state SET retained_bytes=COALESCE((SELECT SUM(bytes) FROM session_journal WHERE session_id=?),0) WHERE session_id=?`, sessionID, sessionID)
	return err
}
