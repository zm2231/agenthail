package registry

import (
	"strings"
	"time"
)

func (r *Registry) BacklogTold(projectKey string) (map[string]bool, error) {
	rows, err := r.read.Query(`SELECT item_id FROM ledger_backlog_told WHERE project_key=?`, projectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	told := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		told[id] = true
	}
	return told, rows.Err()
}

func (r *Registry) MarkBacklogTold(projectKey string, ids []string, at time.Time) error {
	for _, id := range ids {
		if _, err := r.db.Exec(`INSERT OR IGNORE INTO ledger_backlog_told(project_key, item_id, told_at_ms) VALUES (?,?,?)`, projectKey, id, at.UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// KeepBacklogTold forgets every item that is no longer ready, so its next flip to ready is told again.
func (r *Registry) KeepBacklogTold(projectKey string, ready []string) error {
	if len(ready) == 0 {
		_, err := r.db.Exec(`DELETE FROM ledger_backlog_told WHERE project_key=?`, projectKey)
		return err
	}
	args := []any{projectKey}
	for _, id := range ready {
		args = append(args, id)
	}
	_, err := r.db.Exec(`DELETE FROM ledger_backlog_told WHERE project_key=? AND item_id NOT IN (?`+strings.Repeat(",?", len(ready)-1)+`)`, args...)
	return err
}
