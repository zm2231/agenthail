package registry

import "github.com/zm2231/agenthail/internal/surface"

type QueuedSender struct {
	SessionID       string
	SourceSessionID string
}

const queuedSendersQuery = `SELECT DISTINCT session_id,source_session_id FROM message_queue
	WHERE status IN ('pending','inflight') AND source_session_id NOT IN ('',session_id,?,?) AND author!=?`

func (r *Registry) QueuedSenders() ([]QueuedSender, error) {
	rows, err := r.read.Query(queuedSendersQuery, surface.SystemSessionID, surface.PersonSessionID, string(surface.AuthorPerson))
	if err != nil {
		return nil, err
	}
	return appendRows(make([]QueuedSender, 0), rows, func(row rowScanner) (QueuedSender, error) {
		var sender QueuedSender
		return sender, row.Scan(&sender.SessionID, &sender.SourceSessionID)
	})
}
