package registry

import "encoding/json"

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

// HostSessionRef is a paired host's session as this host mirrors it, with the
// alias and project that host gives it.
type HostSessionRef struct {
	SessionRef
	HostID  string
	Alias   string
	Project json.RawMessage
}

func (r *Registry) LiveHostSessions() ([]HostSessionRef, error) {
	return queryRows(r.read, nil, func(row rowScanner) (HostSessionRef, error) {
		var ref HostSessionRef
		var project []byte
		err := row.Scan(&ref.ID, &ref.Order, &ref.HostID, &ref.Alias, &project)
		ref.Project = project
		return ref, err
	}, `SELECT s.id,s.rowid,s.host_id,coalesce(json_extract(cs.projection_fingerprint,'$.alias'),''),cs.host_project FROM sessions s JOIN catalog_sessions cs ON cs.session_id=s.id
		WHERE s.host_id<>'' AND cs.retired=0 AND cs.unavailable_reason='' AND NOT EXISTS (SELECT 1 FROM session_archive a WHERE a.session_id=s.id) ORDER BY s.rowid`)
}

func (r *Registry) SessionDescendants(sessionID string) ([]string, error) {
	return queryRows(r.read, nil, scanString, `WITH RECURSIVE spawned(id) AS (
		SELECT session_id FROM session_origins WHERE creator_session_id=?
		UNION SELECT o.session_id FROM session_origins o JOIN spawned ON o.creator_session_id=spawned.id)
		SELECT s.id FROM spawned JOIN sessions s ON s.id=spawned.id WHERE `+liveLocalSession, sessionID)
}

func (r *Registry) SessionChildren(sessionID string) ([]string, error) {
	return queryRows(r.read, nil, scanString, `SELECT s.id FROM session_origins o JOIN sessions s ON s.id=o.session_id WHERE o.creator_session_id=? AND `+liveLocalSession+` ORDER BY s.rowid`, sessionID)
}

func (r *Registry) RunningChildren(creatorID string) (int, error) {
	var running int
	err := r.read.QueryRow(`SELECT COUNT(*) FROM session_origins o JOIN sessions s ON s.id=o.session_id WHERE o.creator_session_id=? AND (s.status='busy' OR (s.status<>'offline' AND EXISTS (SELECT 1 FROM tasks t WHERE t.child_session_id=s.id AND t.state='running'))) AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.child_session_id=s.id AND t.stalled=1) AND `+liveLocalSession, creatorID).Scan(&running)
	return running, err
}

const maxSpawnerChain = 64

// SessionAncestors is the spawner chain, nearest first; the depth bound ends a creator cycle.
func (r *Registry) SessionAncestors(sessionID string) ([]string, error) {
	return queryRows(r.read, nil, scanString, `WITH RECURSIVE spawners(id, depth) AS (
		SELECT creator_session_id, 1 FROM session_origins WHERE session_id=? AND creator_session_id<>''
		UNION ALL SELECT o.creator_session_id, spawners.depth+1 FROM session_origins o JOIN spawners ON o.session_id=spawners.id WHERE o.creator_session_id<>'' AND spawners.depth<?)
		SELECT id FROM spawners ORDER BY depth`, sessionID, maxSpawnerChain)
}

func (r *Registry) SessionArchived(sessionID string) (bool, error) {
	var archived bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_archive WHERE session_id=?)`, sessionID).Scan(&archived)
	return archived, err
}
