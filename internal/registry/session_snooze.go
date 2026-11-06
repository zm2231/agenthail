package registry

import "time"

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
