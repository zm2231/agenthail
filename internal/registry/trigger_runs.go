package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	TriggerPending     = "pending"
	TriggerRunning     = "running"
	TriggerSucceeded   = "succeeded"
	TriggerFailed      = "failed"
	TriggerTimedOut    = "timed_out"
	TriggerInterrupted = "interrupted"
	TriggerSkipped     = "skipped"

	MaxTriggerBacklog       = 100
	keptFinishedTriggerRuns = 1000
)

var ErrTriggerRunNotFound = errors.New("no such trigger run")

type TriggerRun struct {
	ID         int64           `json:"id"`
	Trigger    string          `json:"trigger"`
	DedupeKey  string          `json:"dedupeKey"`
	SessionID  string          `json:"sessionId"`
	TurnID     string          `json:"turnId"`
	Completion json.RawMessage `json:"-"`
	Test       bool            `json:"test,omitempty"`
	State      string          `json:"state"`
	Attempts   int             `json:"attempts"`
	ExitCode   int             `json:"exitCode"`
	Signal     string          `json:"signal,omitempty"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	Error      string          `json:"error,omitempty"`
	QueuedAt   time.Time       `json:"queuedAt"`
	StartedAt  *time.Time      `json:"startedAt,omitempty"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
}

type TriggerOutcome struct {
	State    string
	ExitCode int
	Signal   string
	Stdout   string
	Stderr   string
	Error    string
}

// RecordCompletion records the turn notice, when there is one, and queues
// the trigger runs the completion matched, in one transaction. A run already
// recorded for that trigger and completion is left as it is, and a trigger
// with a full backlog records the run as skipped.
func (r *Registry) RecordCompletion(sessionID string, notice *TurnNotice, runs []TriggerRun) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if notice != nil {
		if _, err := tx.Exec(`UPDATE session_runtime SET notice_turn_id=?,notice_outcome=?,notice_request_id=?,notice_at_ms=? WHERE session_id=?`, notice.TurnID, notice.Outcome, notice.RequestID, notice.At.UnixMilli(), sessionID); err != nil {
			return err
		}
	}
	now := time.Now().UnixMilli()
	for _, run := range runs {
		var waiting int
		if err := tx.QueryRow(`SELECT count(*) FROM trigger_runs WHERE trigger_name=? AND state=?`, run.Trigger, TriggerPending).Scan(&waiting); err != nil {
			return err
		}
		state, message := TriggerPending, ""
		if waiting >= MaxTriggerBacklog {
			state, message = TriggerSkipped, fmt.Sprintf("backlog full: %d runs waiting", waiting)
		}
		finished := int64(0)
		if state == TriggerSkipped {
			finished = now
		}
		completion := run.Completion
		if len(completion) == 0 {
			completion = json.RawMessage(`{}`)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO trigger_runs(trigger_name,dedupe_key,session_id,turn_id,completion,test,state,error,queued_at_ms,finished_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			run.Trigger, run.DedupeKey, run.SessionID, run.TurnID, string(completion), run.Test, state, message, now, finished); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InterruptTriggerRuns marks every run left running by a daemon that
// stopped. They are not run again unless retried.
func (r *Registry) InterruptTriggerRuns() (int64, error) {
	result, err := r.db.Exec(`UPDATE trigger_runs SET state=?,error='the daemon stopped while it ran',finished_at_ms=? WHERE state=?`, TriggerInterrupted, time.Now().UnixMilli(), TriggerRunning)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *Registry) StartNextTriggerRun(trigger string) (*TriggerRun, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run, err := scanTriggerRun(tx.QueryRow(`SELECT `+triggerRunColumns+` FROM trigger_runs WHERE trigger_name=? AND state=? ORDER BY id LIMIT 1`, trigger, TriggerPending))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := tx.Exec(`UPDATE trigger_runs SET state=?,attempts=attempts+1,started_at_ms=?,finished_at_ms=0,exit_code=0,signal='',stdout='',stderr='',error='' WHERE id=?`, TriggerRunning, now.UnixMilli(), run.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	run.State, run.Attempts, run.StartedAt = TriggerRunning, run.Attempts+1, &now
	return &run, nil
}

func (r *Registry) FinishTriggerRun(id int64, outcome TriggerOutcome, history HistoryEntry) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var trigger string
	if err := tx.QueryRow(`SELECT trigger_name FROM trigger_runs WHERE id=? AND state=?`, id, TriggerRunning).Scan(&trigger); errors.Is(err, sql.ErrNoRows) {
		return ErrTriggerRunNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE trigger_runs SET state=?,exit_code=?,signal=?,stdout=?,stderr=?,error=?,finished_at_ms=? WHERE id=?`,
		outcome.State, outcome.ExitCode, outcome.Signal, outcome.Stdout, outcome.Stderr, boundHistoryText(outcome.Error), time.Now().UnixMilli(), id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM trigger_runs WHERE trigger_name=? AND state NOT IN (?,?) AND id NOT IN (
		SELECT id FROM trigger_runs WHERE trigger_name=? AND state NOT IN (?,?) ORDER BY id DESC LIMIT ?)`,
		trigger, TriggerPending, TriggerRunning, trigger, TriggerPending, TriggerRunning, keptFinishedTriggerRuns); err != nil {
		return err
	}
	if err := recordHistory(tx, history); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) RetryTriggerRun(id int64) (TriggerRun, error) {
	result, err := r.db.Exec(`UPDATE trigger_runs SET state=?,queued_at_ms=?,started_at_ms=0,finished_at_ms=0 WHERE id=? AND state NOT IN (?,?)`, TriggerPending, time.Now().UnixMilli(), id, TriggerPending, TriggerRunning)
	if err != nil {
		return TriggerRun{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		run, err := r.TriggerRun(id)
		if err != nil {
			return TriggerRun{}, err
		}
		return TriggerRun{}, fmt.Errorf("trigger run %d is %s; only a finished run can be retried", id, run.State)
	}
	return r.TriggerRun(id)
}

func (r *Registry) TriggerRun(id int64) (TriggerRun, error) {
	run, err := scanTriggerRun(r.read.QueryRow(`SELECT `+triggerRunColumns+` FROM trigger_runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TriggerRun{}, ErrTriggerRunNotFound
	}
	return run, err
}

func (r *Registry) PendingTriggers() ([]string, error) {
	rows, err := r.read.Query(`SELECT DISTINCT trigger_name FROM trigger_runs WHERE state=? ORDER BY trigger_name`, TriggerPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

type TriggerRunFilter struct {
	Trigger   string
	SessionID string
	Limit     int
}

func (r *Registry) ListTriggerRuns(filter TriggerRunFilter) ([]TriggerRun, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.read.Query(`SELECT `+triggerRunColumns+` FROM trigger_runs WHERE (?='' OR trigger_name=?) AND (?='' OR session_id=?) ORDER BY id DESC LIMIT ?`,
		filter.Trigger, filter.Trigger, filter.SessionID, filter.SessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []TriggerRun{}
	for rows.Next() {
		run, err := scanTriggerRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

const triggerRunColumns = `id,trigger_name,dedupe_key,session_id,turn_id,completion,test,state,attempts,exit_code,signal,stdout,stderr,error,queued_at_ms,started_at_ms,finished_at_ms`

func scanTriggerRun(row interface{ Scan(...any) error }) (TriggerRun, error) {
	var run TriggerRun
	var completion string
	var queued, started, finished int64
	if err := row.Scan(&run.ID, &run.Trigger, &run.DedupeKey, &run.SessionID, &run.TurnID, &completion, &run.Test, &run.State, &run.Attempts, &run.ExitCode, &run.Signal, &run.Stdout, &run.Stderr, &run.Error, &queued, &started, &finished); err != nil {
		return TriggerRun{}, err
	}
	run.Completion = json.RawMessage(completion)
	run.QueuedAt = time.UnixMilli(queued).UTC()
	if started > 0 {
		at := time.UnixMilli(started).UTC()
		run.StartedAt = &at
	}
	if finished > 0 {
		at := time.UnixMilli(finished).UTC()
		run.FinishedAt = &at
	}
	return run, nil
}
