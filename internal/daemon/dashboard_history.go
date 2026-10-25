package daemon

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) dashboardHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	limit := 25
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	maxLimit := 100
	if sessionID != "" {
		maxLimit = 200
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("limit must be between 1 and %d", maxLimit))
			return
		}
		limit = parsed
	}
	var beforeID int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "before must be a positive history id")
			return
		}
		beforeID = parsed
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	queryText := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(kind) > 100 || len(queryText) > 200 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "history filters are too long")
		return
	}
	var entries []registry.HistoryEntry
	hasMore := false
	var err error
	if sessionID != "" {
		if beforeID != 0 || kind != "" || queryText != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a session's history takes only a limit")
			return
		}
		entries, err = d.Registry.ListHistory(limit, sessionID)
	} else {
		entries, hasMore, err = d.Registry.ListHistoryPage(limit, beforeID, kind, queryText)
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("read delivery history: %s", err))
		return
	}
	kinds, err := d.Registry.ListHistoryKinds()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("read delivery history kinds: %s", err))
		return
	}
	items := make([]dashboardHistory, 0, len(entries))
	for _, entry := range entries {
		items = append(items, d.dashboardHistoryEntry(entry))
	}
	nextBefore := int64(0)
	if hasMore && len(entries) > 0 {
		nextBefore = entries[len(entries)-1].ID
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "hasMore": hasMore, "nextBefore": nextBefore, "kinds": kinds})
}

func (d *Daemon) dashboardHistoryEntry(entry registry.HistoryEntry) dashboardHistory {
	return dashboardHistory{ID: entry.ID, CreatedAt: entry.CreatedAt, Kind: entry.Kind, SessionID: d.publicSessionID(entry.SessionID), SourceSessionID: d.publicSessionID(entry.SourceSessionID), Target: d.resolveDisplay(entry.SessionID), Source: d.resolveDisplay(entry.SourceSessionID), QueueID: entry.QueueID, Message: entry.Message, Result: entry.Result, Error: entry.Error, Evidence: entry.Evidence, Operation: entry.Operation}
}
