package registry

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	QueueHistoryWindow    = 30 * 24 * time.Hour
	QueueHistoryPageLimit = 50
	queueHistoryPageMax   = 200
	queuedAtLayout        = "2006-01-02 15:04:05"
)

// The first page carries every active row plus windowed history; later pages walk older history by id unwindowed.
type QueuePageRequest struct {
	SessionID string
	Before    int64
	Limit     int
}

type QueuePage struct {
	Active     []QueueRow
	History    []QueueRow
	NextBefore int64
}

const queueHistoricalWhere = `(q.status IN ('delivered','canceled') OR (q.status='dead' AND q.dead_at_ms<=?))`

func (r *Registry) QueuePage(request QueuePageRequest, adapters surface.Adapters) (QueuePage, error) {
	now := r.now()
	limit := request.Limit
	if limit <= 0 {
		limit = QueueHistoryPageLimit
	}
	limit = min(limit, queueHistoryPageMax)
	mode, err := r.busyMode()
	if err != nil {
		return QueuePage{}, err
	}
	deadCutoff := now.Add(-deadReviewWindow).UnixMilli()
	scope, scopeArgs := "", []any{}
	if request.SessionID != "" {
		scope, scopeArgs = ` AND q.session_id=?`, []any{request.SessionID}
	}
	scan := func(row rowScanner) (QueueRow, error) { return scanQueueRow(row, adapters, now) }
	var page QueuePage
	if request.Before == 0 {
		rows, err := r.read.Query(queueRowSelect(mode)+` WHERE NOT `+queueHistoricalWhere+scope+` ORDER BY q.id`, append([]any{now.UnixMilli(), deadCutoff}, scopeArgs...)...)
		if err != nil {
			return QueuePage{}, err
		}
		if page.Active, err = appendRows(make([]QueueRow, 0), rows, scan); err != nil {
			return QueuePage{}, err
		}
	}
	window, windowArgs := "", []any{}
	if request.Before == 0 {
		window, windowArgs = ` AND q.queued_at>=?`, []any{now.Add(-QueueHistoryWindow).UTC().Format(queuedAtLayout)}
	} else {
		window, windowArgs = ` AND q.id<?`, []any{request.Before}
	}
	args := append(append([]any{now.UnixMilli(), deadCutoff}, scopeArgs...), windowArgs...)
	rows, err := r.read.Query(queueRowSelect(mode)+` WHERE `+queueHistoricalWhere+scope+window+` ORDER BY q.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return QueuePage{}, err
	}
	if page.History, err = appendRows(make([]QueueRow, 0), rows, scan); err != nil {
		return QueuePage{}, err
	}
	cursor := request.Before
	if len(page.History) > 0 {
		cursor = page.History[len(page.History)-1].ID
	} else if cursor == 0 {
		if err := r.read.QueryRow(`SELECT COALESCE(MAX(q.id),0)+1 FROM message_queue q WHERE 1=1`+scope, scopeArgs...).Scan(&cursor); err != nil {
			return QueuePage{}, err
		}
	}
	var older bool
	if err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM message_queue q WHERE `+queueHistoricalWhere+scope+` AND q.id<?)`, append(append([]any{deadCutoff}, scopeArgs...), cursor)...).Scan(&older); err != nil {
		return QueuePage{}, err
	}
	if older {
		page.NextBefore = cursor
	}
	return page, nil
}
