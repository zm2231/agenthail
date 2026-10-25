package daemon

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

func (d *Daemon) dashboardDeliveryDismiss(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if request.DeliveryID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "deliveryId is required")
		return
	}
	dismissed, err := d.Registry.DismissDeliveryProblem(request.DeliveryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, http.StatusNotFound, "not_found", "delivery problem not found")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := d.catalog.flushCommitted(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("publish delivery dismissal: %s", err))
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "dismissed": dismissed})
	return
}

func (d *Daemon) dashboardQueueRetry(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if request.QueueID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "queueId is required")
		return
	}
	item, itemErr := d.Registry.QueueItem(request.QueueID, d.adapters())
	if itemErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", itemErr.Error())
		return
	}
	target, targetErr := d.Registry.Session(item.SessionID)
	if targetErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", targetErr.Error())
		return
	}
	if owner, ownerErr := d.Registry.SessionHost(target.ID); ownerErr != nil || owner == "" {
		adapter, adapterErr := d.adapters().For(target)
		if adapterErr != nil {
			writeAPIError(w, http.StatusConflict, "conflict", "target surface is not configured")
			return
		}
		if err := d.ensureDashboardWritable(r.Context(), adapter, target); err != nil {
			writeAPIError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
	}
	if err := d.Registry.RetryMessage(request.QueueID); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}

func (d *Daemon) dashboardQueueCancel(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if request.QueueID <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "queueId is required")
		return
	}
	if err := d.Registry.CancelMessage(request.QueueID); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}
