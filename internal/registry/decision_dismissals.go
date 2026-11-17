package registry

import "time"

func (r *Registry) DismissDecision(sessionID, stateKey string, at time.Time) error {
	_, err := r.db.Exec(`INSERT INTO decision_dismissals(session_id,state_key,dismissed_at_ms) VALUES(?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET state_key=excluded.state_key,dismissed_at_ms=excluded.dismissed_at_ms`, sessionID, stateKey, at.UnixMilli())
	return err
}

func (r *Registry) DecisionDismissals() (map[string]string, error) {
	rows, err := r.read.Query(`SELECT session_id,state_key FROM decision_dismissals`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dismissed := map[string]string{}
	for rows.Next() {
		var sessionID, stateKey string
		if err := rows.Scan(&sessionID, &stateKey); err != nil {
			return nil, err
		}
		dismissed[sessionID] = stateKey
	}
	return dismissed, rows.Err()
}

const AskDismissed = "dismissed"

func (r *Registry) DismissAsk(id string, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE asks SET state=?,answered_by=?,answered_via=?,answered_at_ms=? WHERE id=? AND state=?`, AskWithdrawn, AskToPerson, AskDismissed, at.UnixMilli(), id, AskOpen)
	if err != nil {
		return false, err
	}
	dismissed, err := result.RowsAffected()
	return dismissed == 1, err
}
