package registry

import (
	"errors"
	"time"
)

const (
	SnoozeWakeTime     = "time"
	SnoozeWakeFinished = "finished"
)

type Snooze struct {
	At    time.Time
	Until time.Time
	Wake  string
}

func (s Snooze) Set() bool { return !s.At.IsZero() }

var ErrSnoozeArchived = errors.New("an archived session cannot be snoozed; unarchive it first")

func (r *Registry) SnoozeSession(sessionID string, snooze Snooze) (previous Snooze, err error) {
	return r.replaceSnooze(sessionID, snooze)
}

func (r *Registry) WakeSession(sessionID string) (previous Snooze, err error) {
	return r.replaceSnooze(sessionID, Snooze{})
}

func (r *Registry) replaceSnooze(sessionID string, snooze Snooze) (Snooze, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return Snooze{}, err
	}
	defer tx.Rollback()
	if snooze.Set() {
		var archived bool
		err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_archive WHERE session_id=?) OR EXISTS(SELECT 1 FROM catalog_sessions cs
 JOIN projects p ON p.id=json_extract(cs.host_project,'$.localId') WHERE cs.session_id=? AND p.archived_at_ms>0)`, sessionID, sessionID).Scan(&archived)
		if err != nil {
			return Snooze{}, err
		}
		if archived {
			return Snooze{}, ErrSnoozeArchived
		}
	}
	var at, until int64
	var wake string
	if err := tx.QueryRow(`SELECT snoozed_at_ms,snoozed_until_ms,snooze_wake FROM sessions WHERE id=?`, sessionID).Scan(&at, &until, &wake); err != nil {
		return Snooze{}, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET snoozed_at_ms=?,snoozed_until_ms=?,snooze_wake=? WHERE id=?`,
		milliOrZero(snooze.At), milliOrZero(snooze.Until), snooze.Wake, sessionID); err != nil {
		return Snooze{}, err
	}
	return scanSnooze(at, until, wake), tx.Commit()
}

func scanSnooze(at, until int64, wake string) Snooze {
	return Snooze{At: unixMilliOrZero(at), Until: unixMilliOrZero(until), Wake: wake}
}
