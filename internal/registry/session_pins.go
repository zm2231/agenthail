package registry

import (
	"database/sql"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type KeepAlive struct {
	Attempts     int
	FirstAttempt time.Time
	NextAttempt  time.Time
	HealthySince time.Time
	FailedAt     time.Time
	Announced    bool
	Error        string
}

type PinnedSession struct {
	Session   surface.Session
	PinnedAt  time.Time
	KeepAlive KeepAlive
}

// A pin asks for the session back, so it also forgets a stop and the keeper's failures.
func (r *Registry) PinSession(sessionID string, at time.Time) (time.Time, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE sessions SET pinned_at_ms=CASE WHEN pinned_at_ms=0 THEN ? ELSE pinned_at_ms END,stopped_at_ms=0 WHERE id=?`, at.UnixMilli(), sessionID)
	if err != nil {
		return time.Time{}, err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return time.Time{}, err
	} else if changed == 0 {
		return time.Time{}, sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM session_keep_alive WHERE session_id=?`, sessionID); err != nil {
		return time.Time{}, err
	}
	var pinned int64
	if err := tx.QueryRow(`SELECT pinned_at_ms FROM sessions WHERE id=?`, sessionID).Scan(&pinned); err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(pinned).UTC(), tx.Commit()
}

func (r *Registry) UnpinSession(sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE sessions SET pinned_at_ms=0 WHERE id=?`, sessionID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM session_keep_alive WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// A stop holds until the session is next active, resumed or pinned.
func (r *Registry) RecordSessionStop(sessionID string, at time.Time) error {
	_, err := r.db.Exec(`UPDATE sessions SET stopped_at_ms=? WHERE id=?`, at.UnixMilli(), sessionID)
	return err
}

func (r *Registry) ClearSessionStop(sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE sessions SET stopped_at_ms=0 WHERE id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM session_keep_alive WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) PinnedSessions() ([]PinnedSession, error) {
	return r.pinnedSessions(``)
}

func (r *Registry) PinnedSession(sessionID string) (PinnedSession, bool, error) {
	sessions, err := r.pinnedSessions(` AND s.id=?`, sessionID)
	if err != nil || len(sessions) == 0 {
		return PinnedSession{}, false, err
	}
	return sessions[0], true, nil
}

func (r *Registry) pinnedSessions(filter string, args ...any) ([]PinnedSession, error) {
	rows, err := r.read.Query(`SELECT s.id,s.pinned_at_ms,COALESCE(k.attempts,0),COALESCE(k.first_attempt_ms,0),COALESCE(k.next_attempt_ms,0),COALESCE(k.healthy_since_ms,0),COALESCE(k.failed_at_ms,0),COALESCE(k.announced,0),COALESCE(k.error,'')
		FROM sessions s LEFT JOIN session_keep_alive k ON k.session_id=s.id
		WHERE s.pinned_at_ms>0 AND (s.stopped_at_ms=0 OR s.stopped_at_ms<s.last_active_ms) AND `+liveLocalSession+filter+` ORDER BY s.rowid`, args...)
	if err != nil {
		return nil, err
	}
	type pinned struct {
		id    string
		at    int64
		state [6]int64
		err   string
	}
	var found []pinned
	for rows.Next() {
		var p pinned
		if err := rows.Scan(&p.id, &p.at, &p.state[0], &p.state[1], &p.state[2], &p.state[3], &p.state[4], &p.state[5], &p.err); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sessions := make([]PinnedSession, 0, len(found))
	for _, p := range found {
		session, err := r.Session(p.id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, PinnedSession{Session: *session, PinnedAt: time.UnixMilli(p.at).UTC(), KeepAlive: KeepAlive{
			Attempts: int(p.state[0]), FirstAttempt: unixMilliOrZero(p.state[1]), NextAttempt: unixMilliOrZero(p.state[2]),
			HealthySince: unixMilliOrZero(p.state[3]), FailedAt: unixMilliOrZero(p.state[4]), Announced: p.state[5] != 0, Error: p.err,
		}})
	}
	return sessions, nil
}

func (r *Registry) SaveKeepAlive(sessionID string, state KeepAlive) error {
	if state == (KeepAlive{}) {
		_, err := r.db.Exec(`DELETE FROM session_keep_alive WHERE session_id=?`, sessionID)
		return err
	}
	_, err := r.db.Exec(`INSERT INTO session_keep_alive(session_id,attempts,first_attempt_ms,next_attempt_ms,healthy_since_ms,failed_at_ms,announced,error)
		SELECT id,?,?,?,?,?,?,? FROM sessions WHERE id=? AND pinned_at_ms>0
		ON CONFLICT(session_id) DO UPDATE SET attempts=excluded.attempts,first_attempt_ms=excluded.first_attempt_ms,next_attempt_ms=excluded.next_attempt_ms,
		healthy_since_ms=excluded.healthy_since_ms,failed_at_ms=excluded.failed_at_ms,announced=excluded.announced,error=excluded.error`,
		state.Attempts, milliOrZero(state.FirstAttempt), milliOrZero(state.NextAttempt), milliOrZero(state.HealthySince), milliOrZero(state.FailedAt), state.Announced, state.Error, sessionID)
	return err
}

type SessionMark struct {
	PinnedAt  time.Time
	KeepAlive KeepAlive
	Snooze    Snooze
}

func (r *Registry) SessionMarks() (map[string]SessionMark, error) {
	rows, err := r.read.Query(`SELECT s.id,s.pinned_at_ms,COALESCE(k.failed_at_ms,0),COALESCE(k.error,''),s.snoozed_at_ms,s.snoozed_until_ms,s.snooze_wake
 FROM sessions s LEFT JOIN session_keep_alive k ON k.session_id=s.id AND s.pinned_at_ms>0
 WHERE (s.pinned_at_ms>0 OR s.snoozed_at_ms>0) AND s.host_id=''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marks := map[string]SessionMark{}
	for rows.Next() {
		var id, wake string
		var pinned, failed, snoozed, until int64
		var mark SessionMark
		if err := rows.Scan(&id, &pinned, &failed, &mark.KeepAlive.Error, &snoozed, &until, &wake); err != nil {
			return nil, err
		}
		mark.PinnedAt, mark.KeepAlive.FailedAt = unixMilliOrZero(pinned), unixMilliOrZero(failed)
		mark.Snooze = scanSnooze(snoozed, until, wake)
		marks[id] = mark
	}
	return marks, rows.Err()
}

func (r *Registry) MarkKeepAliveAnnounced(sessionID string, failedAt time.Time) error {
	_, err := r.db.Exec(`UPDATE session_keep_alive SET announced=1 WHERE session_id=? AND failed_at_ms=?`, sessionID, failedAt.UnixMilli())
	return err
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
