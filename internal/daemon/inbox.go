package daemon

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type inboxPage struct {
	Items      []inboxItem `json:"items"`
	NextBefore int64       `json:"nextBefore,omitempty"`
}

type inboxItem struct {
	ID              int64                         `json:"id"`
	MessageID       string                        `json:"messageId"`
	SessionID       string                        `json:"sessionId"`
	Target          string                        `json:"target"`
	SourceSessionID string                        `json:"sourceSessionId"`
	Source          string                        `json:"source"`
	Message         string                        `json:"message"`
	Truncated       bool                          `json:"truncated,omitempty"`
	Status          registry.DeliveryIntentStatus `json:"status"`
	Evidence        surface.DeliveryEvidence      `json:"evidence"`
	Pending         bool                          `json:"pending"`
	Failure         string                        `json:"failure,omitempty"`
	QueueID         int64                         `json:"queueId,omitempty"`
	QueueStatus     string                        `json:"queueStatus,omitempty"`
	Attempts        int                           `json:"attempts,omitempty"`
	LastError       string                        `json:"lastError,omitempty"`
	Relayed         bool                          `json:"relayed,omitempty"`
	AskID           string                        `json:"askId,omitempty"`
	CreatedAt       string                        `json:"createdAt"`
	UpdatedAt       string                        `json:"updatedAt"`
}

func (d *Daemon) inboxHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for the inbox.")
		return
	}
	query := r.URL.Query()
	request := registry.InboxRequest{SessionID: strings.TrimSpace(query.Get("sessionId"))}
	if raw := query.Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "before must be a positive integer.")
			return
		}
		request.Before = value
	}
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer.")
			return
		}
		request.Limit = value
	}
	page, err := d.Registry.InboxPage(request)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "The inbox is unavailable.")
		return
	}
	items := make([]inboxItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, d.inboxItem(item))
	}
	writeDashboardJSON(w, http.StatusOK, inboxPage{Items: items, NextBefore: page.NextBefore})
}

func (d *Daemon) inboxItem(item registry.InboxItem) inboxItem {
	return inboxItem{
		ID:              item.ID,
		MessageID:       item.UUID,
		SessionID:       d.publicSessionID(item.SessionID),
		Target:          d.resolveDisplay(item.SessionID),
		SourceSessionID: d.publicSessionID(item.SourceSessionID),
		Source:          d.resolveDisplay(item.SourceSessionID),
		Message:         item.Message,
		Truncated:       item.Truncated,
		Status:          item.Status,
		Evidence:        item.Evidence,
		Pending:         item.Pending(),
		Failure:         item.Failure,
		QueueID:         item.QueueID,
		QueueStatus:     item.QueueStatus,
		Attempts:        item.Attempts,
		LastError:       item.LastError,
		Relayed:         item.Relayed,
		AskID:           item.AskID,
		CreatedAt:       item.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:       item.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
