package registry

import "github.com/zm2231/agenthail/internal/surface"

const queueHistoricalWhere = `(q.status IN ('delivered','canceled') OR (q.status='dead' AND q.dead_at_ms<=?))`

func (r *Registry) ActiveQueue(sessionID string, adapters surface.Adapters) ([]QueueRow, error) {
	now := r.now()
	scope, args := "", []any{now.UnixMilli(), now.Add(-deadReviewWindow).UnixMilli()}
	if sessionID != "" {
		scope, args = ` AND q.session_id=?`, append(args, sessionID)
	}
	rows, err := r.read.Query(queueRowSelect(r.busyMode())+` WHERE NOT `+queueHistoricalWhere+scope+` ORDER BY q.id`, args...)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]QueueRow, 0), rows, func(row rowScanner) (QueueRow, error) { return scanQueueRow(row, adapters, now) })
}
