package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) doDeliveryDelete(_ context.Context, p principal, request dashboardActionRequest) (actionResult, error) {
	if p.Kind == principalSession {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "a failed message is deleted by the person")
	}
	if request.DeliveryID <= 0 {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "deliveryId is required")
	}
	deleted, err := d.Registry.DeleteFailedDelivery(request.DeliveryID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "delivery not found")
	case errors.Is(err, registry.ErrDeliveryNotDeletable):
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", err.Error())
	case err != nil:
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if err := d.catalog.flushCommitted(); err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", fmt.Sprintf("publish delivery deletion: %s", err))
	}
	return actionOK(map[string]any{"ok": true, "deleted": deleted}), nil
}

// sendFailed answers a refused send or steer. When the daemon kept the
// message as a failed delivery, the reply names it so the client can delete
// it, and says whether it never reached the session.
func (d *Daemon) sendFailed(status int, code string, err error) error {
	failure := &actionError{Status: status, Code: code, Message: err.Error()}
	var failed delivery.FailedDeliveryError
	if !errors.As(err, &failed) {
		return failure
	}
	deletable, lookupErr := d.Registry.DeliveryDeletable(failed.DeliveryID)
	if lookupErr != nil {
		d.log.Printf("read delivery %d: %s", failed.DeliveryID, lookupErr)
	}
	failure.Extra = map[string]any{"deliveryId": failed.DeliveryID, "deletable": deletable}
	return failure
}
