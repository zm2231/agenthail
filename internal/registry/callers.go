package registry

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/surface"
)

type CallerRecord struct {
	Session  surface.Session
	Alias    string
	Identity string
	HostID   string
	Archived bool
	Roles    SessionRoles
	Ceiling  json.RawMessage
	// Recorded is false until discovery has stored the session's project.
	Recorded bool
	Project  json.RawMessage
}

var callerSelect = sessionSelect(`s.status`) + `,COALESCE((SELECT a.name FROM aliases a WHERE a.session_id=s.id),''),COALESCE((SELECT i.name FROM agent_identities i WHERE i.identity_id=s.id),''),s.host_id,
	EXISTS(SELECT 1 FROM session_archive a WHERE a.session_id=s.id),
	s.id IN (` + localLeadSessions + `),
	s.id IN (` + voiceOperatorSessions + `),
	COALESCE((SELECT c.ceiling FROM session_ceilings c WHERE c.session_id=s.id), '{}'),
	cs.session_id IS NOT NULL,
	COALESCE(cs.host_project, X'')
	FROM sessions s` + sessionJoins + ` WHERE s.id=?`

func (r *Registry) Caller(sessionID string) (CallerRecord, error) {
	var record CallerRecord
	var columns sessionScan
	var ceiling string
	targets := append(columns.targets(), &record.Alias, &record.Identity, &record.HostID, &record.Archived, &record.Roles.Lead, &record.Roles.Operator, &ceiling, &record.Recorded, &record.Project)
	if err := r.callers.QueryRow(callerSelect, sessionID).Scan(targets...); err != nil {
		return CallerRecord{}, err
	}
	session, err := columns.result()
	if err != nil {
		return CallerRecord{}, err
	}
	record.Session, record.Ceiling = session, json.RawMessage(ceiling)
	return record, nil
}

func (r *Registry) CallerProjectSetting(project, name string) (string, error) {
	return projectSettingValue(r.callers, project, name)
}
