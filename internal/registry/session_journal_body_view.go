package registry

import (
	"context"
	"database/sql"
	"fmt"
)

// SessionJournalBodyView is a retained body as one read saw it. Stamp is the
// seq of the body's last whole write; appending to its tail keeps it, so two
// views with the same stamp share every byte the earlier one held.
type SessionJournalBodyView struct {
	Stamp uint64
	Total int
	Head  []byte
	Tail  []byte
}

func (r *Registry) SessionJournalBodyView(sessionID, ref string, head, from int) (SessionJournalBodyView, bool, error) {
	if head < 0 || from < 0 {
		return SessionJournalBodyView{}, false, fmt.Errorf("invalid session journal body view")
	}
	readTx, err := r.read.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SessionJournalBodyView{}, false, err
	}
	defer readTx.Rollback()
	tx := journalTx{Tx: readTx, prepared: r.journalRead}
	var view SessionJournalBodyView
	var baseLength int
	if err := tx.QueryRow(journalBodyStampSQL, sessionID, ref).Scan(&view.Stamp, &baseLength, &view.Total); err != nil {
		if err == sql.ErrNoRows {
			return SessionJournalBodyView{}, false, nil
		}
		return SessionJournalBodyView{}, false, err
	}
	if view.Head, err = bodyRange(tx, sessionID, ref, baseLength, 0, min(head, view.Total)); err != nil {
		return SessionJournalBodyView{}, false, err
	}
	if from < view.Total {
		if view.Tail, err = bodyRange(tx, sessionID, ref, baseLength, from, view.Total); err != nil {
			return SessionJournalBodyView{}, false, err
		}
	}
	return view, true, nil
}

func bodyRange(tx journalWriter, sessionID, ref string, baseLength, lo, hi int) ([]byte, error) {
	out := make([]byte, 0, hi-lo)
	if lo >= hi {
		return out, nil
	}
	if lo < baseLength {
		var part []byte
		if err := tx.QueryRow(journalBodyBaseRangeSQL, lo+1, min(hi, baseLength)-lo, sessionID, ref).Scan(&part); err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	if hi <= baseLength {
		return out, nil
	}
	rows, err := tx.Query(journalBodyChunkRangeSQL, ref, max(lo, baseLength), hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var start int
		var chunk []byte
		if err := rows.Scan(&start, &chunk); err != nil {
			return nil, err
		}
		begin, end := max(lo, start)-start, min(hi, start+len(chunk))-start
		out = append(out, chunk[begin:end]...)
	}
	return out, rows.Err()
}
