package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/zm2231/agenthail/internal/registry"
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
	projectID, ok := d.queueProject(w, r)
	if !ok {
		return
	}
	var rows []registry.QueueRow
	if raw := query.Get("id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "id must be a positive integer.")
			return
		}
		row, err := d.Registry.QueueItem(id, d.adapters())
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
			return
		}
		if row != nil {
			rows = []registry.QueueRow{*row}
		}
	} else {
		var err error
		if rows, err = d.Registry.ActiveQueue(query.Get("sessionId"), d.adapters()); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
			return
		}
	}
	if projectID != "" {
		projects, err := CatalogSessionProjects(d.Registry)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
			return
		}
		rows = projects.In(rows, projectID)
	}
	items := make([]dashboardQueue, 0, len(rows))
	for _, row := range rows {
		items = append(items, d.dashboardQueueItem(row))
	}
	writeDashboardJSON(w, http.StatusOK, mobileQueuePage{Items: items})
}

func (d *Daemon) queueProject(w http.ResponseWriter, r *http.Request) (string, bool) {
	query := r.URL.Query()
	if query.Has("cwd") && query.Has("projectId") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "Choose either cwd or projectId.")
		return "", false
	}
	if !query.Has("cwd") {
		return query.Get("projectId"), true
	}
	projectID, err := ProjectHolding(r.Context(), d.Registry, query.Get("cwd"))
	if errors.Is(err, ErrNoProject) {
		writeAPIError(w, http.StatusNotFound, "not_found", err.Error())
		return "", false
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return "", false
	}
	return projectID, true
}
