package registry

import (
	"database/sql"
	"errors"
	"time"
)

func (r *Registry) PinSession(sessionID string, at time.Time) (time.Time, error) {
	if _, err := r.db.Exec(`UPDATE sessions SET pinned_at_ms=CASE WHEN pinned_at_ms=0 THEN ? ELSE pinned_at_ms END WHERE id=?`, at.UnixMilli(), sessionID); err != nil {
		return time.Time{}, err
	}
	var pinned int64
	if err := r.db.QueryRow(`SELECT pinned_at_ms FROM sessions WHERE id=?`, sessionID).Scan(&pinned); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(pinned).UTC(), nil
}

func (r *Registry) UnpinSession(sessionID string) error {
	result, err := r.db.Exec(`UPDATE sessions SET pinned_at_ms=0 WHERE id=?`, sessionID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Registry) SessionPinnedAt(sessionID string) (time.Time, error) {
	var pinned int64
	err := r.read.QueryRow(`SELECT pinned_at_ms FROM sessions WHERE id=?`, sessionID).Scan(&pinned)
	return unixMilliOrZero(pinned), err
}

type SessionMark struct {
	PinnedAt time.Time
	Snooze   Snooze
}

func (r *Registry) SessionMarks() (map[string]SessionMark, error) {
	rows, err := r.read.Query(`SELECT id,pinned_at_ms,snoozed_at_ms,snoozed_until_ms,snooze_wake
 FROM sessions WHERE (pinned_at_ms>0 OR snoozed_at_ms>0) AND host_id=''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marks := map[string]SessionMark{}
	for rows.Next() {
		var id, wake string
		var pinned, snoozed, until int64
		var mark SessionMark
		if err := rows.Scan(&id, &pinned, &snoozed, &until, &wake); err != nil {
			return nil, err
		}
		mark.PinnedAt = unixMilliOrZero(pinned)
		mark.Snooze = scanSnooze(snoozed, until, wake)
		marks[id] = mark
	}
	return marks, rows.Err()
}

// A new lead inherits its predecessor's pin; only a project's first lead is pinned by default.
func pinNewLeadTx(tx *sql.Tx, projectID, sessionID string, at time.Time) error {
	if sessionID == "" {
		return nil
	}
	pinned := at.UnixMilli()
	var predecessor string
	err := tx.QueryRow(`SELECT session_id FROM coordinator_history WHERE project_id=? AND session_id NOT IN ('',?) ORDER BY id DESC LIMIT 1`, projectID, sessionID).Scan(&predecessor)
	switch {
	case err == nil:
		pinned = 0
		if err := tx.QueryRow(`SELECT pinned_at_ms FROM sessions WHERE id=?`, predecessor).Scan(&pinned); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if pinned == 0 {
		return nil
	}
	_, err = tx.Exec(`UPDATE sessions SET pinned_at_ms=? WHERE id=? AND pinned_at_ms=0`, pinned, sessionID)
	return err
}

func unixMilliOrZero(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func milliOrZero(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}
