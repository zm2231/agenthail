package registry

import "time"

func (r *Registry) QueueNotificationWithdraw(key string, at time.Time) error {
	_, err := r.db.Exec(`INSERT INTO notification_withdrawals(key, queued_at_ms) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET queued_at_ms=MAX(queued_at_ms, excluded.queued_at_ms)`, key, at.UnixMilli())
	return err
}

func (r *Registry) PendingNotificationWithdraws() ([]string, error) {
	rows, err := r.read.Query(`SELECT key FROM notification_withdrawals ORDER BY queued_at_ms, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// A withdraw queued again after started is still owed.
func (r *Registry) ClearNotificationWithdraw(key string, started time.Time) error {
	_, err := r.db.Exec(`DELETE FROM notification_withdrawals WHERE key=? AND queued_at_ms<=?`, key, started.UnixMilli())
	return err
}
