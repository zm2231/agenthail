package registry

import (
	"context"
	"database/sql"
	"time"
)

func catalogPresentationOutgoing(ctx context.Context, tx *sql.Tx) (map[string]int, time.Time, error) {
	rows, err := tx.QueryContext(ctx, `SELECT di.sender_session_id,di.status,di.updated_at FROM delivery_intents di
 LEFT JOIN message_queue mq ON mq.id=di.notification_queue_id
 WHERE di.status IN ('failed','held') AND COALESCE(di.dismissed_at,'')='' AND COALESCE(mq.status,'')!='delivered' AND NOT (`+settledDeliveryFailure+`)`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	result := map[string]int{}
	var refresh time.Time
	for rows.Next() {
		var sender, status, updated string
		if err := rows.Scan(&sender, &status, &updated); err != nil {
			return nil, refresh, err
		}
		if deliveryAttentionSession(sender, false) == "" {
			continue
		}
		result[sender]++
		if status == "failed" {
			at, err := parseSQLiteTimestamp(updated)
			if err != nil {
				return nil, refresh, err
			}
			at = at.Add(24*time.Hour + time.Second)
			if refresh.IsZero() || at.Before(refresh) {
				refresh = at
			}
		}
	}
	return result, refresh, rows.Err()
}
