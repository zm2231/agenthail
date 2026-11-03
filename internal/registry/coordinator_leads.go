package registry

import "database/sql"

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

func leadSessionTx(tx *sql.Tx, sessionID string) (bool, error) {
	var lead bool
	err := tx.QueryRow(`SELECT EXISTS(`+localLeadSessions+` AND a.session_id=?)`, sessionID).Scan(&lead)
	return lead, err
}
