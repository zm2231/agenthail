package daemon

import (
	"context"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// withdrawReplacedRequest settles an open request of a lead a restart
// replaced, so it stops waiting on the person: a permission prompt is denied
// and a question dismissed, both with a note saying why.
func (d *Daemon) withdrawReplacedRequest(ctx context.Context, adapter surface.Surface, session *surface.Session, requestID, actor string) (actionResult, error) {
	if requestID == "" {
		return actionResult{}, invalidRequest("requestId is required")
	}
	coord := d.coordinator()
	current, replaced := coord.ReplacedBy(session.ID)
	if !replaced {
		return actionResult{}, actionFailed(http.StatusConflict, "not_replaced", "only a request from a lead that was replaced can be withdrawn; answer it instead")
	}
	ask, found, err := d.Registry.Ask(requestID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !found || ask.SessionID != session.ID || ask.Origin != registry.AskOriginPermission || ask.State != registry.AskOpen {
		return actionResult{}, staleRequest()
	}
	spec, _ := coord.Of(session.ID)
	note := fmt.Sprintf("Withdrawn: you are no longer coordinator @%s; conversation %s took over. The person did not answer this request.", spec.Name, current)
	return d.answerPermission(ctx, adapter, session, ask, surface.Decision{Behavior: "deny", Message: note}, registry.AskToPerson, actor)
}
