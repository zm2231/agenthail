package registry

import (
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	InboxPageLimit = 50
	inboxPageMax   = 200
)

type InboxRequest struct {
	SessionID string
	Before    int64
	Limit     int
}

type InboxPage struct {
	Items      []InboxItem
	NextBefore int64
}

// InboxItem is one message a session was sent. Message is the queued text
// when the message waited, else the intent's copy, which Truncated marks as
// cut at the intent's limit.
type InboxItem struct {
	ID              int64
	UUID            string
	SessionID       string
	SenderSessionID string
	SourceSessionID string
	Message         string
	Truncated       bool
	Status          DeliveryIntentStatus
	Evidence        surface.DeliveryEvidence
	Failure         string
	QueueID         int64
	Attempts        int
	LastError       string
	Relayed         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (i InboxItem) Pending() bool {
	return i.Status == DeliveryIntentSubmitted || i.Status == DeliveryIntentQueued || i.Status == DeliveryIntentHeld
}

func (r *Registry) InboxPage(request InboxRequest) (InboxPage, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = InboxPageLimit
	}
	limit = min(limit, inboxPageMax)
	query := `SELECT i.id,i.uuid,i.target_session_id,i.sender_session_id,COALESCE(q.source_session_id,''),COALESCE(q.message,i.message),q.id IS NULL AND length(i.message)>=?,i.status,i.evidence,i.failure,i.queue_id,COALESCE(q.attempts,0),COALESCE(q.last_error,''),i.relayed,i.created_at,i.updated_at
		FROM delivery_intents i LEFT JOIN message_queue q ON i.queue_id>0 AND q.id=i.queue_id WHERE 1=1`
	args := []any{maxIntentMessage}
	if request.SessionID != "" {
		query += ` AND i.target_session_id=?`
		args = append(args, request.SessionID)
	}
	if request.Before > 0 {
		query += ` AND i.id<?`
		args = append(args, request.Before)
	}
	rows, err := r.read.Query(query+` ORDER BY i.id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return InboxPage{}, err
	}
	items, err := appendRows(make([]InboxItem, 0, limit+1), rows, scanInboxItem)
	if err != nil {
		return InboxPage{}, err
	}
	page := InboxPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextBefore = page.Items[limit-1].ID
	}
	return page, nil
}

func scanInboxItem(row rowScanner) (InboxItem, error) {
	var item InboxItem
	var status, evidence, createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.UUID, &item.SessionID, &item.SenderSessionID, &item.SourceSessionID, &item.Message, &item.Truncated, &status, &evidence, &item.Failure, &item.QueueID, &item.Attempts, &item.LastError, &item.Relayed, &createdAt, &updatedAt); err != nil {
		return InboxItem{}, err
	}
	if item.SourceSessionID == "" {
		item.SourceSessionID = item.SenderSessionID
	}
	item.Status, item.Evidence = DeliveryIntentStatus(status), surface.DeliveryEvidence(evidence)
	var err error
	if item.CreatedAt, err = parseSQLiteTimestamp(createdAt); err != nil {
		return InboxItem{}, fmt.Errorf("parse inbox creation timestamp: %w", err)
	}
	if item.UpdatedAt, err = parseSQLiteTimestamp(updatedAt); err != nil {
		return InboxItem{}, fmt.Errorf("parse inbox update timestamp: %w", err)
	}
	return item, nil
}
