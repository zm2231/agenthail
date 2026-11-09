package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/zm2231/agenthail/internal/registry"
)

type triggersPage struct {
	Status triggerStatus         `json:"status"`
	Runs   []registry.TriggerRun `json:"runs"`
}

type triggerActionRequest struct {
	Action  string `json:"action"`
	Trigger string `json:"trigger"`
	RunID   int64  `json:"runId"`
}

func (d *Daemon) apiTriggersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d.listTriggers(w, r)
	case http.MethodPost:
		d.triggerAction(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST for triggers.")
	}
}

func (d *Daemon) listTriggers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := registry.TriggerRunFilter{Trigger: query.Get("trigger"), SessionID: query.Get("sessionId"), Limit: 50}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "limit must be 1 to 500.")
			return
		}
		filter.Limit = limit
	}
	status, err := d.triggerStatus()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Trigger status is unavailable.")
		return
	}
	runs, err := d.Registry.ListTriggerRuns(filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Trigger runs are unavailable.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, triggersPage{Status: status, Runs: runs})
}

// triggerAction reloads, retries or tests triggers. Each one runs a command
// the operator configured, so only this machine's CLI may ask.
func (d *Daemon) triggerAction(w http.ResponseWriter, r *http.Request) {
	if !dashboardBearer(r.Context()) {
		writeAPIError(w, http.StatusForbidden, "local_only", "Triggers can be reloaded, retried and tested only from this machine's agenthail CLI.")
		return
	}
	var request triggerActionRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The body must be a trigger action.")
		return
	}
	switch request.Action {
	case "reload":
		if err := d.reloadTriggers(); err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
			return
		}
		status, err := d.triggerStatus()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Trigger status is unavailable.")
			return
		}
		writeDashboardJSON(w, http.StatusOK, status)
	case "retry":
		run, err := d.Registry.RetryTriggerRun(request.RunID)
		if errors.Is(err, registry.ErrTriggerRunNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		d.dispatchTriggers()
		writeDashboardJSON(w, http.StatusOK, run)
	case "test":
		run, err := d.testTrigger(request.Trigger)
		if errors.Is(err, errNoSuchTrigger) {
			writeAPIError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "The test run could not be queued.")
			return
		}
		writeDashboardJSON(w, http.StatusOK, run)
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "action must be reload, retry or test.")
	}
}
