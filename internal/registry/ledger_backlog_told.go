package registry

import "time"

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

// MarkBacklogTold returns the ids this call marked, so of two callers telling at once only one tells each item; on error it marks none.
func (r *Registry) MarkBacklogTold(projectKey string, ids []string, at time.Time) ([]string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var marked []string
	for _, id := range ids {
		result, err := tx.Exec(`INSERT OR IGNORE INTO ledger_backlog_told(project_key, item_id, told_at_ms) VALUES (?,?,?)`, projectKey, id, at.UnixMilli())
		if err != nil {
			return nil, err
		}
		if n, err := result.RowsAffected(); err != nil {
			return nil, err
		} else if n == 1 {
			marked = append(marked, id)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return marked, nil
}

func (r *Registry) KeepBacklogTold(projectKey string, ready []string) error {
	told, err := r.BacklogTold(projectKey)
	if err != nil {
		return err
	}
	for _, id := range ready {
		delete(told, id)
	}
	for id := range told {
		if _, err := r.db.Exec(`DELETE FROM ledger_backlog_told WHERE project_key=? AND item_id=?`, projectKey, id); err != nil {
			return err
		}
	}
	return nil
}
