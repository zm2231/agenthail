package ledger

import (
	"fmt"
	"time"
)

type Grant struct {
	ProjectKey string
	SessionID  string
	Since      time.Time
}

func (s *Store) GrantSession(projectKey, sessionID string) error {
	if projectKey == "" || sessionID == "" {
		return fmt.Errorf("project key and session are required")
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO ledger_grants(project_key,session_id,since_ms) VALUES(?,?,?)`, projectKey, sessionID, time.Now().UnixMilli())
	return err
}

func (s *Store) RevokeSession(projectKey, sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM ledger_grants WHERE project_key=? AND session_id=?`, projectKey, sessionID)
	return err
}

func (s *Store) SessionGrants(sessionID string) ([]Grant, error) {
	rows, err := s.read.Query(`SELECT project_key,session_id,since_ms FROM ledger_grants WHERE session_id=? ORDER BY project_key`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var grants []Grant
	for rows.Next() {
		var g Grant
		var since int64
		if err := rows.Scan(&g.ProjectKey, &g.SessionID, &since); err != nil {
			return nil, err
		}
		g.Since = time.UnixMilli(since).UTC()
		grants = append(grants, g)
	}
	return grants, rows.Err()
}
