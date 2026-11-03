package daemon

import (
	"context"
	"net/http"
	"strings"
)

func (d *Daemon) doNotionCreate(ctx context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	name, err := d.reserveName(strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"))
	if err != nil {
		return actionResult{}, err
	}
	defer func() { _ = d.Registry.ReleaseAliasReservation(name) }()
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	receipt, createErr := d.createNotionThread(ctx, request.Message, name, request.Model)
	if createErr != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", createErr.Error())
	}
	return actionResult{Status: http.StatusCreated, Body: map[string]any{"ok": true, "result": receipt, "sessionId": receipt.SessionID}}, nil
}
