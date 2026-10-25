package daemon

import (
	"context"
	"net/http"
	"strings"
)

func (d *Daemon) dashboardNotionCreate(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	name, reserved := d.reserveStartName(w, strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"))
	if !reserved {
		return
	}
	defer func() { _ = d.Registry.ReleaseAliasReservation(name) }()
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	receipt, createErr := d.createNotionThread(ctx, request.Message, name, request.Model)
	if createErr != nil {
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", createErr.Error())
		return
	}
	writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "result": receipt, "sessionId": receipt.SessionID})
	return
}
