package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

func (d *Daemon) doDeliveryDismiss(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if request.DeliveryID <= 0 {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "deliveryId is required")
	}
	dismissed, err := d.Registry.DismissDeliveryProblem(request.DeliveryID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "delivery problem not found")
		}
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	if err := d.catalog.flushCommitted(); err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", fmt.Sprintf("publish delivery dismissal: %s", err))
	}
	return actionOK(map[string]any{"ok": true, "dismissed": dismissed}), nil
}

func (d *Daemon) doQueueRetry(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if request.QueueID <= 0 {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "queueId is required")
	}
	item, itemErr := d.Registry.QueueItem(request.QueueID, d.adapters())
	if itemErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", itemErr.Error())
	}
	target, targetErr := d.Registry.Session(item.SessionID)
	if targetErr != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", targetErr.Error())
	}
	if owner, ownerErr := d.Registry.SessionHost(target.ID); ownerErr != nil || owner == "" {
		adapter, adapterErr := d.adapters().For(target)
		if adapterErr != nil {
			return actionResult{}, actionFailed(http.StatusConflict, "conflict", "target surface is not configured")
		}
		if err := d.ensureDashboardWritable(ctx, adapter, target); err != nil {
			return actionResult{}, actionFailed(http.StatusConflict, "conflict", err.Error())
		}
	}
	if err := d.Registry.RetryMessage(request.QueueID); err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true}), nil
}

func (d *Daemon) doQueueClear(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	sessionID, err := d.Registry.ResolveTarget(request.SessionID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve session: %s", err))
	}
	canceled, err := d.Registry.CancelMessagesForSession(sessionID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "canceled": canceled}), nil
}

func (d *Daemon) doQueueCancel(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if request.QueueID <= 0 {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "queueId is required")
	}
	canceled, err := d.Registry.CancelMessage(request.QueueID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	result := map[string]any{"message": canceled.Message}
	if attachments := d.handBack(canceled.Uploads); attachments != nil {
		result["attachments"] = attachments
	}
	return actionOK(map[string]any{"ok": true, "result": result}), nil
}
