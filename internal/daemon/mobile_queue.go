package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

type mobileQueuePage struct {
	Items []dashboardQueue `json:"items"`
}

func (d *Daemon) mobileQueueHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for queued instructions.")
		return
	}
	query := r.URL.Query()
	if raw := query.Get("id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "id must be a positive integer.")
			return
		}
		d.mobileQueueItem(w, id)
		return
	}
	rows, err := d.Registry.ActiveQueue(query.Get("sessionId"), d.adapters())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
		return
	}
	items := make([]dashboardQueue, 0, len(rows))
	for _, row := range rows {
		items = append(items, d.dashboardQueueItem(row))
	}
	writeDashboardJSON(w, http.StatusOK, mobileQueuePage{Items: items})
}

func (d *Daemon) mobileQueueItem(w http.ResponseWriter, id int64) {
	row, err := d.Registry.QueueItem(id, d.adapters())
	if errors.Is(err, sql.ErrNoRows) {
		writeDashboardJSON(w, http.StatusOK, mobileQueuePage{Items: []dashboardQueue{}})
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, mobileQueuePage{Items: []dashboardQueue{d.dashboardQueueItem(*row)}})
}
