package daemon

import (
	"fmt"
	"net/http"
)

func (d *Daemon) dashboardRelayAdd(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if request.FromID == "" || request.ToID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "fromId and toId are required")
		return
	}
	fromID, fromErr := d.Registry.ResolveTarget(request.FromID)
	if fromErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve source agent: %s", fromErr))
		return
	}
	toID, toErr := d.resolveLinkTarget(request.ToID)
	if toErr != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve destination agent: %s", toErr))
		return
	}
	toSession, toSessionErr := d.Registry.Session(toID)
	if toSessionErr != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "destination session not found")
		return
	}
	if !d.hostOwned(toSession.ID) {
		adapter, adapterErr := d.adapters().For(toSession)
		if adapterErr != nil {
			writeAPIError(w, http.StatusConflict, "conflict", "destination surface is not configured")
			return
		}
		if err := d.ensureDashboardWritable(r.Context(), adapter, toSession); err != nil {
			writeAPIError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
	}
	pattern := request.Pattern
	if pattern == "" {
		pattern = ".*"
	}
	fromSession, err := d.Registry.Session(fromID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "source session not found")
		return
	}
	fromAdapter, fromAdapterErr := d.adapters().For(fromSession)
	if fromAdapterErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", "source surface is not configured")
		return
	}
	if err := d.Registry.PrepareRelaySource(r.Context(), fromAdapter, fromSession, request.Handoff); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if _, err := d.Registry.AddRelayRoute(fromID, toID, pattern, request.Once, request.Handoff); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}

func (d *Daemon) dashboardRelayRemove(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	if err := d.Registry.RemoveRoute(request.RelayID); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}
