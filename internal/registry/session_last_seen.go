package registry

import (
	"database/sql"
	"errors"
	"time"
)

// SessionLastSeen is the later of when the session was registered and its last recorded activity.
func (r *Registry) SessionLastSeen(id string) (time.Time, error) {
	var registered string
	var lastActiveMS int64
	if err := r.read.QueryRow(`SELECT registered_at,last_active_ms FROM sessions WHERE id=?`, id).Scan(&registered, &lastActiveMS); err != nil {
		return time.Time{}, err
	}
	seen, err := parseRegisteredAt(registered)
	if err != nil {
		return time.Time{}, err
	}
	if active := time.UnixMilli(lastActiveMS); lastActiveMS > 0 && active.After(seen) {
		seen = active
	}
	return seen, nil
}

func (r *Registry) SessionRegisteredAt(id string) (time.Time, error) {
	var registered string
	err := r.read.QueryRow(`SELECT registered_at FROM sessions WHERE id=?`, id).Scan(&registered)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return parseRegisteredAt(registered)
}

func parseRegisteredAt(registered string) (time.Time, error) {
	return time.ParseInLocation(time.DateTime, registered, time.UTC)
}

func formatRegisteredAt(at time.Time) string {
	return at.UTC().Format(time.DateTime)
}
