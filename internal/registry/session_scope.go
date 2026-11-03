package registry

const liveLocalSession = `s.host_id='' AND NOT EXISTS (SELECT 1 FROM session_archive a WHERE a.session_id=s.id)`

func (r *Registry) LiveLocalSessionIDs() ([]string, error) {
	return queryRows(r.read, nil, scanString, `SELECT s.id FROM sessions s WHERE `+liveLocalSession+` ORDER BY s.last_active_ms DESC, s.id`)
}

func (r *Registry) SessionDescendants(sessionID string) ([]string, error) {
	return queryRows(r.read, nil, scanString, `WITH RECURSIVE spawned(id) AS (
		SELECT session_id FROM session_origins WHERE creator_session_id=?
		UNION SELECT o.session_id FROM session_origins o JOIN spawned ON o.creator_session_id=spawned.id)
		SELECT s.id FROM spawned JOIN sessions s ON s.id=spawned.id WHERE `+liveLocalSession+` ORDER BY s.last_active_ms DESC, s.id`, sessionID)
}
