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

const QueueStatusRemoved = "removed"

// InboxItem is one message a session was sent. Message is the queued text
// when the message waited, else the intent's copy, which Truncated marks as
// cut at the intent's limit. A queue row canceled or removed without its
// intent being told settles the item here, so the intent keeps its status
// and raises no delivery problem.
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
	QueueStatus     string
	Attempts        int
	LastError       string
	Relayed         bool
	AskID           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (i InboxItem) Pending() bool {
	switch i.QueueStatus {
	case "canceled", "delivered", QueueStatusRemoved:
		return false
	}
	return intentPending(i.Status)
}

func intentPending(status DeliveryIntentStatus) bool {
	return status == DeliveryIntentSubmitted || status == DeliveryIntentQueued || status == DeliveryIntentHeld
}

func (i *InboxItem) settleFromQueue(queueEvidence surface.DeliveryEvidence) {
	if !intentPending(i.Status) {
		return
	}
	switch i.QueueStatus {
	case "canceled":
		i.Evidence = surface.EvidenceCanceled
	case QueueStatusRemoved:
		i.Evidence = surface.EvidenceUnknown
	case "delivered":
		i.Evidence = queueEvidence
	}
}

func (r *Registry) InboxPage(request InboxRequest) (InboxPage, error) {
	limit := request.Limit
	if limit <= 0 {
		limit = InboxPageLimit
	}
	limit = min(limit, inboxPageMax)
	query := `SELECT i.id,i.uuid,i.target_session_id,i.sender_session_id,COALESCE(q.source_session_id,''),COALESCE(q.message,i.message),q.id IS NULL AND length(i.message)>=?,i.status,i.evidence,i.failure,i.queue_id,CASE WHEN i.queue_id>0 AND q.id IS NULL THEN 'removed' ELSE COALESCE(q.status,'') END,COALESCE(q.evidence,''),COALESCE(q.attempts,0),COALESCE(q.last_error,''),i.relayed,i.created_at,i.updated_at
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
	links, err := r.deliveryAskLinks()
	if err != nil {
		return InboxPage{}, err
	}
	for i := range items {
		items[i].AskID = links.intent[items[i].ID]
		if items[i].AskID == "" && items[i].QueueID > 0 {
			items[i].AskID = links.queue[items[i].QueueID]
		}
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
	var status, evidence, queueEvidence, createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.UUID, &item.SessionID, &item.SenderSessionID, &item.SourceSessionID, &item.Message, &item.Truncated, &status, &evidence, &item.Failure, &item.QueueID, &item.QueueStatus, &queueEvidence, &item.Attempts, &item.LastError, &item.Relayed, &createdAt, &updatedAt); err != nil {
		return InboxItem{}, err
	}
	if item.SourceSessionID == "" {
		item.SourceSessionID = item.SenderSessionID
	}
	item.Status, item.Evidence = DeliveryIntentStatus(status), surface.DeliveryEvidence(evidence)
	item.settleFromQueue(surface.DeliveryEvidence(queueEvidence))
	var err error
	if item.CreatedAt, err = parseSQLiteTimestamp(createdAt); err != nil {
		return InboxItem{}, fmt.Errorf("parse inbox creation timestamp: %w", err)
	}
	if item.UpdatedAt, err = parseSQLiteTimestamp(updatedAt); err != nil {
		return InboxItem{}, fmt.Errorf("parse inbox update timestamp: %w", err)
	}
	return item, nil
}
