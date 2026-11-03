package registry

import "time"

// SessionLastSeen is the later of when the session was registered and its last recorded activity.
func (r *Registry) SessionLastSeen(id string) (time.Time, error) {
	var registered string
	var lastActiveMS int64
	if err := r.read.QueryRow(`SELECT registered_at,last_active_ms FROM sessions WHERE id=?`, id).Scan(&registered, &lastActiveMS); err != nil {
		return time.Time{}, err
	}
	seen, err := time.ParseInLocation(time.DateTime, registered, time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	if active := time.UnixMilli(lastActiveMS); lastActiveMS > 0 && active.After(seen) {
		seen = active
	}
	return seen, nil
}
