package registry

import (
	"database/sql"
	"fmt"
	"strings"
)

func (r *Registry) ReadSessionJournalPage(sessionID string, before uint64, limit int) (SessionJournalPage, error) {
	if strings.TrimSpace(sessionID) == "" || limit < 1 || limit > 200 {
		return SessionJournalPage{}, fmt.Errorf("invalid journal page request")
	}
	tx, err := r.read.Begin()
	if err != nil {
		return SessionJournalPage{}, err
	}
	defer tx.Rollback()
	var earliest, latest, latestSeq sql.NullInt64
	if err := tx.QueryRow(`SELECT (SELECT MIN(position) FROM session_journal WHERE session_id=?),
		(SELECT MAX(position) FROM session_journal WHERE session_id=?),
		(SELECT MAX(seq) FROM session_journal WHERE session_id=?)`, sessionID, sessionID, sessionID).Scan(&earliest, &latest, &latestSeq); err != nil {
		return SessionJournalPage{}, err
	}
	var prunedBefore, historyBefore sql.NullInt64
	if err := tx.QueryRow(`SELECT pruned_before,history_before FROM session_journal_state WHERE session_id=?`, sessionID).Scan(&prunedBefore, &historyBefore); err != nil && err != sql.ErrNoRows {
		return SessionJournalPage{}, err
	}
	if before > 0 {
		historyUnknown := !historyBefore.Valid || historyBefore.Int64 < 0
		if !earliest.Valid || (prunedBefore.Valid && uint64(prunedBefore.Int64) >= before-1) || (historyUnknown && before <= uint64(earliest.Int64)) {
			var earliestPosition, latestPosition uint64
			if earliest.Valid {
				earliestPosition = uint64(earliest.Int64)
			}
			if latest.Valid {
				latestPosition = uint64(latest.Int64)
			}
			return SessionJournalPage{}, &SessionJournalHistoryGapError{Earliest: earliestPosition, Latest: latestPosition}
		}
		earliestPosition, latestPosition := uint64(earliest.Int64), uint64(latest.Int64)
		if before < earliestPosition-1 {
			return SessionJournalPage{}, &SessionJournalHistoryGapError{Earliest: earliestPosition, Latest: latestPosition}
		}
	}
	query := `SELECT session_id,seq,position,kind,provider_key,payload,observed_at,bytes,body_ref FROM session_journal WHERE session_id=?`
	args := []any{sessionID}
	if before > 0 {
		query += ` AND position<?`
		args = append(args, before)
	}
	query += ` ORDER BY position DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return SessionJournalPage{}, err
	}
	entries, err := appendRows(make([]SessionJournalEntry, 0, limit+1), rows, scanSessionJournalEntry)
	if err != nil {
		return SessionJournalPage{}, err
	}
	var next uint64
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[len(entries)-1].Position
	} else if ((prunedBefore.Valid && prunedBefore.Int64 > 0) || !historyBefore.Valid || historyBefore.Int64 < 0) && len(entries) > 0 && entries[len(entries)-1].Position == uint64(earliest.Int64) {
		next = entries[len(entries)-1].Position
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
	page := SessionJournalPage{Entries: entries, NextBefore: next, LatestSeq: uint64(latestSeq.Int64)}
	if next == 0 && (!prunedBefore.Valid || prunedBefore.Int64 == 0) && historyBefore.Valid && historyBefore.Int64 > 0 {
		page.HistoryBefore = historyBefore.Int64
	}
	return page, nil
}
