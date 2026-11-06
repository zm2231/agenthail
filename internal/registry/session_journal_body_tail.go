package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrSessionJournalBodyMoved = errors.New("session journal body is not where the append starts")

type bodyQuerier interface {
	QueryRow(string, ...any) *sql.Row
	Query(string, ...any) (*sql.Rows, error)
}

func (r *Registry) AppendSessionJournalBody(input SessionJournalEntry, from int, tail []byte, retention SessionJournalRetention) (SessionJournalEntry, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.Kind) == "" || input.ProviderKey == "" || input.BodyRef == "" || len(tail) == 0 {
		return SessionJournalEntry{}, fmt.Errorf("session journal body append requires session, kind, provider key, body ref and bytes")
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now().UTC()
	}
	tx, err := r.beginJournal()
	if err != nil {
		return SessionJournalEntry{}, err
	}
	defer tx.Rollback()
	existing, found, err := sessionJournalByProviderKey(tx, input.SessionID, input.ProviderKey)
	if err != nil {
		return SessionJournalEntry{}, err
	}
	if !found || existing.BodyRef != input.BodyRef {
		return SessionJournalEntry{}, ErrSessionJournalBodyMoved
	}
	var stored int
	if err := tx.QueryRow(journalBodyStoredLengthSQL, input.BodyRef, input.SessionID).Scan(&stored); err != nil {
		if err == sql.ErrNoRows {
			return SessionJournalEntry{}, ErrSessionJournalBodyMoved
		}
		return SessionJournalEntry{}, err
	}
	if stored != from {
		return SessionJournalEntry{}, ErrSessionJournalBodyMoved
	}
	input.Payload = append([]byte(nil), input.Payload...)
	input.Bytes = len(input.Payload) + from + len(tail)
	if input.Bytes > retention.Bytes {
		return SessionJournalEntry{}, fmt.Errorf("%w: %d > %d", ErrSessionJournalEntryTooLarge, input.Bytes, retention.Bytes)
	}
	var next int64
	if err := tx.QueryRow(journalNextSeqSQL, input.SessionID).Scan(&next); err != nil {
		return SessionJournalEntry{}, err
	}
	input.Seq, input.Position = uint64(next+1), existing.Position
	if _, err := tx.Exec(journalBodyAppendTailSQL, input.BodyRef, input.SessionID, from, tail); err != nil {
		return SessionJournalEntry{}, err
	}
	if _, err := tx.Exec(journalUpdateAppendedRowSQL, input.Seq, input.Kind, input.Payload, formatTimestamp(input.ObservedAt), input.Bytes, journalRole(input.Kind, input.Payload), input.SessionID, existing.Seq); err != nil {
		return SessionJournalEntry{}, err
	}
	if _, err := tx.Exec(journalStateAdvanceSQL, input.Seq, input.Bytes-existing.Bytes, input.SessionID); err != nil {
		return SessionJournalEntry{}, err
	}
	if err := trimSessionJournal(tx, input.SessionID, retention); err != nil {
		return SessionJournalEntry{}, err
	}
	return input, tx.Commit()
}

func readSessionJournalFullBody(q bodyQuerier, sessionID, ref string) ([]byte, bool, error) {
	var body []byte
	if err := q.QueryRow(journalBodySQL, sessionID, ref).Scan(&body); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	rows, err := q.Query(journalBodyTailSQL, ref)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var chunk []byte
		if err := rows.Scan(&chunk); err != nil {
			return nil, false, err
		}
		body = append(body, chunk...)
	}
	return body, true, rows.Err()
}

func deleteSessionJournalBody(tx journalWriter, sessionID, ref string) error {
	if _, err := tx.Exec(journalBodyClearTailSQL, sessionID, ref); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM session_journal_bodies WHERE session_id=? AND ref=?`, sessionID, ref)
	return err
}
