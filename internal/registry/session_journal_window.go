package registry

import (
	"database/sql"
	"fmt"
	"strings"
)

func (r *Registry) SessionJournalAfter(sessionID string, after uint64, limit int) (SessionJournalWindow, error) {
	if strings.TrimSpace(sessionID) == "" {
		return SessionJournalWindow{}, fmt.Errorf("session id is required")
	}
	if limit < 1 {
		return SessionJournalWindow{Entries: []SessionJournalEntry{}}, nil
	}
	var earliest, latest sql.NullInt64
	var prunedSeq, nextSeq uint64
	if err := r.read.QueryRow(`SELECT (SELECT MIN(seq) FROM session_journal WHERE session_id=?),
		(SELECT MAX(seq) FROM session_journal WHERE session_id=?),COALESCE((SELECT pruned_seq FROM session_journal_state WHERE session_id=?),0),COALESCE((SELECT next_seq FROM session_journal_state WHERE session_id=?),0)`, sessionID, sessionID, sessionID, sessionID).Scan(&earliest, &latest, &prunedSeq, &nextSeq); err != nil {
		return SessionJournalWindow{}, err
	}
	window := SessionJournalWindow{Entries: []SessionJournalEntry{}}
	if !earliest.Valid || !latest.Valid {
		window.Gap = after > 0 && (after < prunedSeq || after > nextSeq)
		return window, nil
	}
	window.EarliestSeq = uint64(earliest.Int64)
	window.LatestSeq = uint64(latest.Int64)
	window.Gap = after > 0 && (after < prunedSeq || after > window.LatestSeq)
	if window.Gap {
		return window, nil
	}
	rows, err := r.read.Query(`SELECT session_id,seq,position,kind,provider_key,payload,observed_at,bytes,body_ref FROM session_journal WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?`, sessionID, after, limit)
	if err != nil {
		return SessionJournalWindow{}, err
	}
	entries, err := appendRows(window.Entries, rows, scanSessionJournalEntry)
	if err != nil {
		return SessionJournalWindow{}, err
	}
	window.Entries = entries
	return window, nil
}
