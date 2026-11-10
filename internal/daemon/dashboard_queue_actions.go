package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func (d *Daemon) doQueueClear(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if strings.TrimSpace(request.SessionID) == "" {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "sessionId is required")
	}
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
	if item, err := d.Registry.QueueItem(request.QueueID, d.adapters()); err == nil && item.Status == "dead" {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", "this message failed; answer its ask instead")
	}
	canceled, err := d.Registry.CancelMessage(request.QueueID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"message": canceled.Message, "attachments": d.handBack(canceled.Uploads)}}), nil
}
