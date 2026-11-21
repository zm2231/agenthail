package registry

import (
	"database/sql"
	"errors"
	"time"
)

type StaleJournal struct {
	SessionID string
	Epoch     string
	Bytes     int64
}

const staleJournalSQL = `SELECT st.session_id,st.source_epoch,st.retained_bytes FROM session_journal_state st
	JOIN sessions s ON s.id=st.session_id
	JOIN session_journal j ON j.session_id=st.session_id
		AND j.seq=(SELECT MAX(seq) FROM session_journal WHERE session_id=st.session_id)
	WHERE s.ended=1 AND j.observed_at<?1
	AND st.session_id NOT IN (SELECT json_extract(u.payload,'$.targetId') FROM ledger_events u WHERE u.type='utterance'
		AND json_extract(u.payload,'$.targetId') IS NOT NULL AND json_extract(u.payload,'$.utteranceId') IN (SELECT COALESCE(json_extract(c.payload,'$.sourceUtteranceId'),json_extract(c.payload,'$.utteranceId'))
			FROM ledger_events c WHERE c.type IN ('clause','clause.source')))`

func (r *Registry) StaleJournalSessions(before time.Time) ([]StaleJournal, error) {
	rows, err := r.read.Query(staleJournalSQL, formatTimestamp(before))
	if err != nil {
		return nil, err
	}
	return appendRows([]StaleJournal{}, rows, func(row rowScanner) (StaleJournal, error) {
		var stale StaleJournal
		err := row.Scan(&stale.SessionID, &stale.Epoch, &stale.Bytes)
		return stale, err
	})
}

// next_seq moves past every old seq so a cursor from before the drop gets a gap, not a replay.
func (r *Registry) DropStaleJournal(stale StaleJournal, before time.Time) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var still StaleJournal
	err = tx.QueryRow(staleJournalSQL+` AND st.session_id=?2`, formatTimestamp(before), stale.SessionID).Scan(&still.SessionID, &still.Epoch, &still.Bytes)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && still.Epoch != stale.Epoch) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, table := range []string{"session_journal", "session_journal_body_tail", "session_journal_bodies"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE session_id=?`, stale.SessionID); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`UPDATE session_journal_state SET retained_bytes=0,next_seq=next_seq+1,pruned_seq=next_seq+1,pruned_before=0,history_before=-1,
		seed_status=?,seed_seq=0,seed_identity='',transcript_checkpoint='',derived_version=0 WHERE session_id=?`, SessionJournalSeedUnknown, stale.SessionID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
