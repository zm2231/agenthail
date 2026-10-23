package registry

import (
	"database/sql"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// LimitResumeState is where the resume of a session a usage limit stopped
// stands. Every state but released holds the session's queue, so nothing is
// sent to it automatically: a waiting stop is resumed when its limit resets,
// a canceled one only when the person resumes it, and a lapsed one, whose
// reset passed too long ago or never came, likewise.
type LimitResumeState string

const (
	LimitResumeWaiting  LimitResumeState = "waiting"
	LimitResumeReleased LimitResumeState = "released"
	LimitResumeCanceled LimitResumeState = "canceled"
	LimitResumeLapsed   LimitResumeState = "lapsed"
)

// LimitResume is one session whose last turn a usage limit refused.
type LimitResume struct {
	SessionID  string
	TurnID     string
	AccountKey string
	RefusedAt  time.Time
	ResetsAt   time.Time
	State      LimitResumeState
	Attempts   int
	UpdatedAt  time.Time
}

// limitHeldFilter matches a session whose limit stop holds its queue.
const limitHeldFilter = `EXISTS(SELECT 1 FROM limit_resumes lr WHERE lr.session_id=%s AND lr.state!='released')`

// RecordLimitStop records that session's last turn was refused by a usage
// limit on account. The same refused turn keeps its state and attempts. A
// new refused turn waits again; when it refuses a resume, the resume's
// attempts carry over, so retries are counted across refusals. A later reset
// time also becomes the reset of every other session waiting on the account,
// since they share the limit.
func (r *Registry) RecordLimitStop(session surface.Session, account string, stop surface.LimitStop) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err := tx.Exec(`INSERT INTO limit_resumes(session_id,turn_id,account_key,refused_at_ms,resets_at_ms,state,attempts,updated_at_ms)
		VALUES (?,?,?,?,?,'waiting',0,?)
		ON CONFLICT(session_id) DO UPDATE SET
			state=CASE WHEN limit_resumes.turn_id=excluded.turn_id THEN limit_resumes.state ELSE 'waiting' END,
			attempts=CASE WHEN limit_resumes.turn_id=excluded.turn_id OR limit_resumes.state='released' THEN limit_resumes.attempts ELSE 0 END,
			updated_at_ms=CASE WHEN limit_resumes.turn_id=excluded.turn_id THEN limit_resumes.updated_at_ms ELSE excluded.updated_at_ms END,
			resets_at_ms=CASE WHEN limit_resumes.turn_id=excluded.turn_id AND excluded.resets_at_ms=0 THEN limit_resumes.resets_at_ms ELSE excluded.resets_at_ms END,
			turn_id=excluded.turn_id,account_key=excluded.account_key,refused_at_ms=excluded.refused_at_ms`,
		session.ID, stop.TurnID, account, unixMilli(stop.RefusedAt), unixMilli(stop.ResetsAt), now); err != nil {
		return err
	}
	if !stop.ResetsAt.IsZero() {
		if _, err := tx.Exec(`UPDATE limit_resumes SET resets_at_ms=? WHERE account_key=? AND session_id!=? AND state='waiting' AND resets_at_ms<?`,
			stop.ResetsAt.UnixMilli(), account, session.ID, stop.ResetsAt.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearLimitStop forgets a stop once something ran after turnID; a stop for
// a later refused turn stays.
func (r *Registry) ClearLimitStop(sessionID, turnID string) error {
	_, err := r.db.Exec(`DELETE FROM limit_resumes WHERE session_id=? AND turn_id=?`, sessionID, turnID)
	return err
}

// TransitionLimitResume moves the stop of turnID to state when it is still
// in one of from, and counts a release as an attempt. It reports whether the
// stop moved: a stop another caller moved first, or one that has moved on to
// another refused turn, is left as it is.
func (r *Registry) TransitionLimitResume(sessionID, turnID string, from []LimitResumeState, state LimitResumeState, now time.Time) (bool, error) {
	return transitionLimitResume(r.db, sessionID, turnID, from, state, now)
}

// ReleaseLimitStop releases the stop of turnID when it is still in one of
// from. When notice is set and the session has nothing queued, the notice is
// queued under deliveryKey in the same transaction, so a message queued at
// the same moment is never joined by a notice. It reports whether the stop
// was released.
func (r *Registry) ReleaseLimitStop(sessionID, turnID string, from []LimitResumeState, notice, deliveryKey string, now time.Time) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	released, err := transitionLimitResume(tx, sessionID, turnID, from, LimitResumeReleased, now)
	if err != nil || !released {
		return false, err
	}
	var queueID int64
	if notice != "" {
		var queued int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight')`, sessionID).Scan(&queued); err != nil {
			return false, err
		}
		if queued == 0 {
			if queueID, _, err = enqueueMessageTx(tx, r.now(), sessionID, notice, deliveryKey, surface.SendOptions{}, 0, QueueOperationMessage, false); err != nil {
				return false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if queueID != 0 {
		_ = r.RecordHistory(HistoryEntry{Kind: "queued", SessionID: sessionID, QueueID: queueID, Message: notice})
	}
	return true, nil
}

func transitionLimitResume(db interface {
	Exec(string, ...any) (sql.Result, error)
}, sessionID, turnID string, from []LimitResumeState, state LimitResumeState, now time.Time) (bool, error) {
	if len(from) == 0 {
		return false, nil
	}
	args := []any{state, state, now.UnixMilli(), sessionID, turnID}
	for _, previous := range from {
		args = append(args, previous)
	}
	result, err := db.Exec(`UPDATE limit_resumes SET state=?, attempts=attempts+CASE WHEN ?='released' THEN 1 ELSE 0 END, updated_at_ms=?
		WHERE session_id=? AND turn_id=? AND state IN (?`+strings.Repeat(",?", len(from)-1)+`)`, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// LimitResumes returns every recorded stop, oldest refusal first.
func (r *Registry) LimitResumes() ([]LimitResume, error) {
	rows, err := r.read.Query(`SELECT session_id,turn_id,account_key,refused_at_ms,resets_at_ms,state,attempts,updated_at_ms FROM limit_resumes ORDER BY refused_at_ms,session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var resumes []LimitResume
	for rows.Next() {
		var resume LimitResume
		var refused, resets, updated int64
		if err := rows.Scan(&resume.SessionID, &resume.TurnID, &resume.AccountKey, &refused, &resets, &resume.State, &resume.Attempts, &updated); err != nil {
			return nil, err
		}
		resume.RefusedAt, resume.ResetsAt, resume.UpdatedAt = fromUnixMilli(refused), fromUnixMilli(resets), fromUnixMilli(updated)
		resumes = append(resumes, resume)
	}
	return resumes, rows.Err()
}

// LimitResumeFor returns the session's recorded stop.
func (r *Registry) LimitResumeFor(sessionID string) (LimitResume, bool, error) {
	var resume LimitResume
	var refused, resets, updated int64
	err := r.read.QueryRow(`SELECT session_id,turn_id,account_key,refused_at_ms,resets_at_ms,state,attempts,updated_at_ms FROM limit_resumes WHERE session_id=?`, sessionID).
		Scan(&resume.SessionID, &resume.TurnID, &resume.AccountKey, &refused, &resets, &resume.State, &resume.Attempts, &updated)
	if err == sql.ErrNoRows {
		return LimitResume{}, false, nil
	}
	if err != nil {
		return LimitResume{}, false, err
	}
	resume.RefusedAt, resume.ResetsAt, resume.UpdatedAt = fromUnixMilli(refused), fromUnixMilli(resets), fromUnixMilli(updated)
	return resume, true, nil
}

func unixMilli(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

func fromUnixMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
