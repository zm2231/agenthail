package registry

import (
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type QueuedMessage struct {
	surface.TurnOptions
	ID              int64
	SessionID       string
	Message         string
	Model           string
	SourceSessionID string
	Attempts        int
	RelayHops       int
	Operation       QueueOperation
	BusyDelivery    string
	Author          surface.Author
	Uploads         surface.Uploads
	PersonName      string
}

const (
	maxHistoryText = 16 * 1024
	maxHistoryRows = 2000
)

type HistoryEntry struct {
	ID              int64                    `json:"id"`
	CreatedAt       string                   `json:"createdAt"`
	Kind            string                   `json:"kind"`
	SessionID       string                   `json:"sessionId,omitempty"`
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	RouteID         int64                    `json:"routeId,omitempty"`
	QueueID         int64                    `json:"queueId,omitempty"`
	CompletionID    string                   `json:"completionId,omitempty"`
	Message         string                   `json:"message,omitempty"`
	Result          string                   `json:"result,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Evidence        surface.DeliveryEvidence `json:"evidence,omitempty"`
	Operation       QueueOperation           `json:"operation,omitempty"`
}

func boundHistoryText(value string) string {
	if len(value) <= maxHistoryText {
		return value
	}
	return value[:maxHistoryText] + "\n[truncated]"
}

func (r *Registry) RecordHistory(entry HistoryEntry) error {
	if entry.Kind == "" {
		return fmt.Errorf("history kind is required")
	}
	res, err := r.db.Exec(`INSERT INTO delivery_history(kind,session_id,source_session_id,route_id,queue_id,completion_id,message,result,error,operation) VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,COALESCE(NULLIF(?10,''),(SELECT operation FROM message_queue WHERE id=?5 AND ?5>0),''))`,
		entry.Kind, entry.SessionID, entry.SourceSessionID, entry.RouteID, entry.QueueID, entry.CompletionID,
		boundHistoryText(entry.Message), boundHistoryText(entry.Result), boundHistoryText(entry.Error), entry.Operation)
	if err != nil {
		return err
	}
	if id, idErr := res.LastInsertId(); idErr == nil {
		_, _ = r.db.Exec(`DELETE FROM delivery_history WHERE id <= ?`, id-maxHistoryRows)
	}
	return nil
}

func (r *Registry) ListHistory(limit int, sessionID string) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `SELECT h.id,h.created_at,h.kind,h.session_id,h.source_session_id,h.route_id,h.queue_id,h.completion_id,h.message,h.result,h.error,h.operation FROM delivery_history h`
	args := []any{}
	if sessionID != "" {
		query += ` WHERE h.session_id=? OR h.source_session_id=?`
		args = append(args, sessionID, sessionID)
	}
	query += ` ORDER BY h.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]HistoryEntry, 0), rows, scanHistoryEntry)
}

func scanHistoryEntry(row rowScanner) (HistoryEntry, error) {
	var entry HistoryEntry
	err := row.Scan(&entry.ID, &entry.CreatedAt, &entry.Kind, &entry.SessionID, &entry.SourceSessionID, &entry.RouteID, &entry.QueueID, &entry.CompletionID, &entry.Message, &entry.Result, &entry.Error, &entry.Operation)
	entry.Evidence = historyEvidence(entry)
	return entry, err
}

func (r *Registry) ListHistoryPage(limit int, beforeID int64, kind, queryText string) ([]HistoryEntry, bool, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	query := `SELECT h.id,h.created_at,h.kind,h.session_id,h.source_session_id,h.route_id,h.queue_id,h.completion_id,h.message,h.result,h.error,h.operation FROM delivery_history h`
	args := []any{}
	conditions := []string{}
	if beforeID > 0 {
		conditions = append(conditions, `h.id < ?`)
		args = append(args, beforeID)
	}
	if kind != "" {
		conditions = append(conditions, `h.kind = ?`)
		args = append(args, kind)
	}
	if queryText != "" {
		conditions = append(conditions, `(h.kind LIKE ? ESCAPE '\' OR h.session_id LIKE ? ESCAPE '\' OR h.source_session_id LIKE ? ESCAPE '\' OR h.message LIKE ? ESCAPE '\' OR h.result LIKE ? ESCAPE '\' OR h.error LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM aliases a WHERE (a.session_id=h.session_id OR a.session_id=h.source_session_id) AND ('@' || a.name) LIKE ? ESCAPE '\') OR EXISTS (SELECT 1 FROM sessions s WHERE (s.id=h.session_id OR s.id=h.source_session_id) AND (s.surface || '/' || s.name) LIKE ? ESCAPE '\'))`)
		pattern := "%" + escapeLike(queryText) + "%"
		for range 8 {
			args = append(args, pattern)
		}
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY h.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	entries, err := appendRows(make([]HistoryEntry, 0, limit+1), rows, scanHistoryEntry)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}

func historyEvidence(entry HistoryEntry) surface.DeliveryEvidence {
	switch entry.Kind {
	case "submitted":
		return surface.EvidenceSubmitted
	case "queued":
		return surface.EvidenceQueued
	case "peer_sent", "peer_received", "transport-accepted":
		return surface.EvidenceTransportAccepted
	case "peer_receipt":
		status := strings.ToLower(strings.TrimSpace(strings.SplitN(entry.Result, ":", 2)[0]))
		switch status {
		case "held":
			return surface.EvidenceHeld
		case "delivered", "received", "accepted":
			return surface.EvidenceDelivered
		case "denied", "failed", "rejected":
			return surface.EvidenceFailed
		default:
			return surface.EvidenceUnknown
		}
	case "sent", "delivered", "control-accepted":
		return surface.EvidenceDelivered
	case "reply", "completed":
		return surface.EvidenceReplyObserved
	case "unknown", "ack-error":
		return surface.EvidenceUnknown
	case "failed", "peer_rejected", "control-failed":
		return surface.EvidenceFailed
	case "canceled":
		return surface.EvidenceCanceled
	default:
		return ""
	}
}

func (r *Registry) ListHistoryKinds() ([]string, error) {
	return queryRows(r.read, []string{}, scanString, `SELECT DISTINCT kind FROM delivery_history ORDER BY kind`)
}
