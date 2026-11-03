package daemon

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

type mobileQueuePage struct {
	Items      []dashboardQueue `json:"items"`
	NextBefore int64            `json:"nextBefore,omitempty"`
}

func (d *Daemon) mobileQueueHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for queued instructions.")
		return
	}
	query := r.URL.Query()
	numbers := map[string]int64{}
	for _, name := range []string{"id", "before", "limit"} {
		if raw := query.Get(name); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value <= 0 {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", name+" must be a positive integer.")
				return
			}
			numbers[name] = value
		}
	}
	if id := numbers["id"]; id != 0 {
		d.mobileQueueItem(w, id)
		return
	}
	_, sessionID, _ := hosts.Split(query.Get("sessionId"))
	page, err := d.Registry.QueuePage(registry.QueuePageRequest{SessionID: sessionID, Before: numbers["before"], Limit: int(numbers["limit"])}, d.adapters())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
		return
	}
	items := make([]dashboardQueue, 0, len(page.Active)+len(page.History))
	for _, row := range page.Active {
		items = append(items, d.dashboardQueueItem(row))
	}
	for _, row := range page.History {
		items = append(items, d.dashboardQueueItem(row))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID > items[j].ID })
	writeDashboardJSON(w, http.StatusOK, mobileQueuePage{Items: items, NextBefore: page.NextBefore})
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
