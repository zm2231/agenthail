package registry

const liveLocalSession = `s.host_id='' AND NOT EXISTS (SELECT 1 FROM session_archive a WHERE a.session_id=s.id)`

// SessionRef orders sessions by registration, which never changes for a session and puts new ones last.
type SessionRef struct {
	ID    string
	Order int64
}

func (r *Registry) LiveLocalSessions() ([]SessionRef, error) {
	return queryRows(r.read, nil, func(row rowScanner) (SessionRef, error) {
		var ref SessionRef
		err := row.Scan(&ref.ID, &ref.Order)
		return ref, err
	}, `SELECT s.id,s.rowid FROM sessions s WHERE `+liveLocalSession+` ORDER BY s.rowid`)
}

func (r *Registry) SessionDescendants(sessionID string) ([]string, error) {
	return queryRows(r.read, nil, scanString, `WITH RECURSIVE spawned(id) AS (
		SELECT session_id FROM session_origins WHERE creator_session_id=?
		UNION SELECT o.session_id FROM session_origins o JOIN spawned ON o.creator_session_id=spawned.id)
		SELECT s.id FROM spawned JOIN sessions s ON s.id=spawned.id WHERE `+liveLocalSession, sessionID)
}
