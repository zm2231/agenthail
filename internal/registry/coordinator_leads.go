package registry

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
)

const localLeadSessions = `SELECT a.session_id FROM coordinators c JOIN aliases a ON a.name=c.name WHERE c.host_id=''`

// LocalLeadSessions are the sessions leading this host's projects, which stay current however long they are idle.
func (r *Registry) LocalLeadSessions() (map[string]bool, error) {
	rows, err := r.read.Query(localLeadSessions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	leads := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		leads[id] = true
	}
	return leads, rows.Err()
}

// LeadsFingerprint changes whenever any process changes a coordinator or the alias that names its session.
func (r *Registry) LeadsFingerprint() (string, error) {
	rows, err := r.read.Query(`SELECT c.project_id, c.host_id, c.project_root, c.name, c.surface, c.cwd, c.model, c.permission_mode, c.instructions, c.session_id, c.changed_by, c.changed_at_ms, COALESCE(a.session_id, '')
		FROM coordinators c LEFT JOIN aliases a ON a.name=c.name ORDER BY c.project_id, c.host_id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	sum := sha256.New()
	for rows.Next() {
		fields := make([]string, 13)
		targets := make([]any, len(fields))
		for i := range fields {
			targets[i] = &fields[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return "", err
		}
		encoded, _ := json.Marshal(fields)
		sum.Write(append(encoded, '\n'))
	}
	return hex.EncodeToString(sum.Sum(nil)), rows.Err()
}

func leadSessionTx(tx *sql.Tx, sessionID string) (bool, error) {
	var lead bool
	err := tx.QueryRow(`SELECT EXISTS(`+localLeadSessions+` AND a.session_id=?)`, sessionID).Scan(&lead)
	return lead, err
}
