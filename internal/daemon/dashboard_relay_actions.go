package daemon

import (
	"context"
	"fmt"
	"net/http"
)

func (d *Daemon) doRelayAdd(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if request.FromID == "" || request.ToID == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "fromId and toId are required")
	}
	fromID, fromErr := d.Registry.ResolveTarget(request.FromID)
	if fromErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve source agent: %s", fromErr))
	}
	toID, toErr := d.resolveLinkTarget(request.ToID)
	if toErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve destination agent: %s", toErr))
	}
	toSession, toSessionErr := d.Registry.Session(toID)
	if toSessionErr != nil {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "destination session not found")
	}
	if !d.hostOwned(toSession.ID) {
		adapter, adapterErr := d.adapters().For(toSession)
		if adapterErr != nil {
			return actionResult{}, actionFailed(http.StatusConflict, "conflict", "destination surface is not configured")
		}
		if err := d.ensureDashboardWritable(ctx, adapter, toSession); err != nil {
			return actionResult{}, actionFailed(http.StatusConflict, "conflict", err.Error())
		}
	}
	pattern := request.Pattern
	if pattern == "" {
		pattern = ".*"
	}
	fromSession, err := d.Registry.Session(fromID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "source session not found")
	}
	fromAdapter, fromAdapterErr := d.adapters().For(fromSession)
	if fromAdapterErr != nil {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", "source surface is not configured")
	}
	if err := d.Registry.PrepareRelaySource(ctx, fromAdapter, fromSession, request.Handoff); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if _, err := d.Registry.AddRelayRoute(fromID, toID, pattern, request.Once, request.Handoff); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}

func (d *Daemon) doRelayRemove(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if err := d.Registry.RemoveRoute(request.RelayID); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}
